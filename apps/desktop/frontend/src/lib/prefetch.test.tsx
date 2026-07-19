import type { ReactNode } from 'react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

// ---------------------------------------------------------------------------
// Mocks
// ---------------------------------------------------------------------------

const getThreadMock = vi.hoisted(() => vi.fn());
vi.mock('@/lib/api', () => ({
  api: {
    getThread: (...args: unknown[]) => getThreadMock(...args),
  },
  // Real-mode branch: prefetches must hit the API path here so the tests can
  // observe exactly which threads are fetched.
  orMock: (real: () => unknown) => real(),
}));

vi.mock('@/lib/mock', () => ({
  mockThread: vi.fn(() => null),
}));

const onlineState = vi.hoisted(() => ({ value: true }));
vi.mock('@/lib/offline', () => ({
  isOnline: () => onlineState.value,
}));

vi.mock('@/lib/server-config', () => ({
  isDemoMode: () => false,
}));

import { usePrefetchNeighbors, useThreadHoverPrefetch } from '@/lib/prefetch';

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function makeItems(ids: string[]) {
  return ids.map((id) => ({ id }) as { id: string } & Record<string, unknown>);
}

function createWrapper(queryClient: QueryClient) {
  return function Wrapper({ children }: { children: ReactNode }) {
    return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
  };
}

function prefetchedIds(): string[] {
  return getThreadMock.mock.calls.map((call) => call[0] as string);
}

let queryClient: QueryClient;

beforeEach(() => {
  vi.useFakeTimers();
  vi.clearAllMocks();
  onlineState.value = true;
  getThreadMock.mockResolvedValue({ thread: null, messages: [] });
  queryClient = new QueryClient();
});

afterEach(() => {
  vi.useRealTimers();
});

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

describe('useThreadHoverPrefetch (desktop)', () => {
  it('does not prefetch when the hover ends before the delay', async () => {
    const { result } = renderHook(() => useThreadHoverPrefetch(), {
      wrapper: createWrapper(queryClient),
    });

    result.current.onHoverStart('t1');
    await vi.advanceTimersByTimeAsync(79);
    result.current.onHoverEnd();
    await vi.advanceTimersByTimeAsync(500);

    expect(getThreadMock).not.toHaveBeenCalled();
  });

  it('sustained hover prefetches exactly once per id', async () => {
    const { result } = renderHook(() => useThreadHoverPrefetch(), {
      wrapper: createWrapper(queryClient),
    });

    result.current.onHoverStart('t1');
    await vi.advanceTimersByTimeAsync(80);
    expect(prefetchedIds()).toEqual(['t1']);

    // Re-hover while the entry is still fresh: no second request.
    result.current.onHoverEnd();
    result.current.onHoverStart('t1');
    await vi.advanceTimersByTimeAsync(200);
    expect(getThreadMock).toHaveBeenCalledTimes(1);
  });

  it('does not prefetch while offline', async () => {
    onlineState.value = false;
    const { result } = renderHook(() => useThreadHoverPrefetch(), {
      wrapper: createWrapper(queryClient),
    });

    result.current.onHoverStart('t1');
    await vi.advanceTimersByTimeAsync(200);

    expect(getThreadMock).not.toHaveBeenCalled();
  });
});

describe('usePrefetchNeighbors (desktop)', () => {
  it('prefetches idx±1 and idx±2 around the selection, never the selection itself', async () => {
    const items = makeItems(['t1', 't2', 't3', 't4', 't5', 't6']);

    renderHook(() => usePrefetchNeighbors(items, 't3'), {
      wrapper: createWrapper(queryClient),
    });
    await vi.advanceTimersByTimeAsync(80);

    expect(prefetchedIds().sort()).toEqual(['t1', 't2', 't4', 't5']);
  });

  it('debounces rapid cursor movement down to the settled selection', async () => {
    const items = makeItems(['t1', 't2', 't3', 't4', 't5', 't6', 't7', 't8', 't9']);
    const { rerender } = renderHook(({ sel }) => usePrefetchNeighbors(items, sel), {
      wrapper: createWrapper(queryClient),
      initialProps: { sel: 't1' },
    });

    await vi.advanceTimersByTimeAsync(10);
    rerender({ sel: 't4' });
    await vi.advanceTimersByTimeAsync(10);
    rerender({ sel: 't8' });
    await vi.advanceTimersByTimeAsync(80);

    expect(prefetchedIds().sort()).toEqual(['t6', 't7', 't9']);
  });

  it('skips neighbors whose cache entry is still fresh', async () => {
    const items = makeItems(['t1', 't2', 't3', 't4', 't5']);
    queryClient.setQueryData(['thread', 't2'], { thread: null, messages: [] });

    renderHook(() => usePrefetchNeighbors(items, 't3'), {
      wrapper: createWrapper(queryClient),
    });
    await vi.advanceTimersByTimeAsync(80);

    expect(prefetchedIds().sort()).toEqual(['t1', 't4', 't5']);
  });

  it('does not prefetch while offline', async () => {
    const items = makeItems(['t1', 't2', 't3']);
    onlineState.value = false;

    renderHook(() => usePrefetchNeighbors(items, 't2'), {
      wrapper: createWrapper(queryClient),
    });
    await vi.advanceTimersByTimeAsync(200);

    expect(getThreadMock).not.toHaveBeenCalled();
  });
});
