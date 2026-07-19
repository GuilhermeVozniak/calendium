import type { ReactNode } from 'react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

// ---------------------------------------------------------------------------
// Mocks
// ---------------------------------------------------------------------------

const onlineState = vi.hoisted(() => ({ value: true }));
vi.mock('@/lib/offline/connectivity', () => ({
  isOnline: () => onlineState.value,
}));

// The hooks must share useThreadDetail's exact queryFn — mocking it here both
// isolates the tests from the whole data layer and asserts the sharing.
const fetchThreadDetailMock = vi.hoisted(() => vi.fn());
vi.mock('@/lib/use-mail', () => ({
  fetchThreadDetail: (...args: unknown[]) => fetchThreadDetailMock(...args),
}));

import {
  useNextPagePrefetch,
  usePrefetchNeighbors,
  useThreadHoverPrefetch,
} from '@/lib/use-prefetch';

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

/** Minimal stand-ins — the hooks only read `id`. */
function makeItems(ids: string[]) {
  return ids.map((id) => ({ id }) as { id: string } & Record<string, unknown>);
}

function createWrapper(queryClient: QueryClient) {
  return function Wrapper({ children }: { children: ReactNode }) {
    return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
  };
}

function prefetchedIds(): string[] {
  return fetchThreadDetailMock.mock.calls.map((call) => call[0] as string);
}

let queryClient: QueryClient;

beforeEach(() => {
  vi.useFakeTimers();
  vi.clearAllMocks();
  onlineState.value = true;
  fetchThreadDetailMock.mockResolvedValue({ thread: null, messages: [], source: 'api' });
  queryClient = new QueryClient();
});

afterEach(() => {
  vi.useRealTimers();
});

// ---------------------------------------------------------------------------
// useThreadHoverPrefetch
// ---------------------------------------------------------------------------

describe('useThreadHoverPrefetch', () => {
  it('does not prefetch when the hover ends before the delay', async () => {
    const { result } = renderHook(() => useThreadHoverPrefetch(), {
      wrapper: createWrapper(queryClient),
    });

    result.current.onHoverStart('t1');
    await vi.advanceTimersByTimeAsync(79);
    result.current.onHoverEnd();
    await vi.advanceTimersByTimeAsync(500);

    expect(fetchThreadDetailMock).not.toHaveBeenCalled();
  });

  it('sustained hover prefetches exactly once per id (fresh cache is not refetched)', async () => {
    const { result } = renderHook(() => useThreadHoverPrefetch(), {
      wrapper: createWrapper(queryClient),
    });

    result.current.onHoverStart('t1');
    await vi.advanceTimersByTimeAsync(80);

    expect(fetchThreadDetailMock).toHaveBeenCalledTimes(1);
    expect(fetchThreadDetailMock).toHaveBeenCalledWith('t1');

    // Hovering the same row again while its cache entry is still fresh must
    // not fire a second request (no prefetch storm on mouse jitter).
    result.current.onHoverEnd();
    result.current.onHoverStart('t1');
    await vi.advanceTimersByTimeAsync(200);

    expect(fetchThreadDetailMock).toHaveBeenCalledTimes(1);
  });

  it('moving to another row restarts the debounce for the new id only', async () => {
    const { result } = renderHook(() => useThreadHoverPrefetch(), {
      wrapper: createWrapper(queryClient),
    });

    result.current.onHoverStart('t1');
    await vi.advanceTimersByTimeAsync(40);
    result.current.onHoverStart('t2');
    await vi.advanceTimersByTimeAsync(80);

    expect(prefetchedIds()).toEqual(['t2']);
  });

  it('does not prefetch while offline', async () => {
    onlineState.value = false;
    const { result } = renderHook(() => useThreadHoverPrefetch(), {
      wrapper: createWrapper(queryClient),
    });

    result.current.onHoverStart('t1');
    await vi.advanceTimersByTimeAsync(200);

    expect(fetchThreadDetailMock).not.toHaveBeenCalled();
  });
});

// ---------------------------------------------------------------------------
// usePrefetchNeighbors
// ---------------------------------------------------------------------------

describe('usePrefetchNeighbors', () => {
  it('prefetches idx±1 and idx±2 around the selection, never the selection itself', async () => {
    const items = makeItems(['t1', 't2', 't3', 't4', 't5', 't6']);

    renderHook(() => usePrefetchNeighbors(items, 't3'), {
      wrapper: createWrapper(queryClient),
    });
    await vi.advanceTimersByTimeAsync(80);

    expect(prefetchedIds().sort()).toEqual(['t1', 't2', 't4', 't5']);
  });

  it('clamps at the list edges', async () => {
    const items = makeItems(['t1', 't2', 't3']);

    renderHook(() => usePrefetchNeighbors(items, 't1'), {
      wrapper: createWrapper(queryClient),
    });
    await vi.advanceTimersByTimeAsync(80);

    expect(prefetchedIds().sort()).toEqual(['t2', 't3']);
  });

  it('debounces rapid selection changes down to the settled selection', async () => {
    const items = makeItems(['t1', 't2', 't3', 't4', 't5', 't6', 't7', 't8', 't9']);
    const { rerender } = renderHook(({ sel }) => usePrefetchNeighbors(items, sel), {
      wrapper: createWrapper(queryClient),
      initialProps: { sel: 't1' },
    });

    // j-spam: selection sweeps before any debounce window elapses.
    await vi.advanceTimersByTimeAsync(10);
    rerender({ sel: 't4' });
    await vi.advanceTimersByTimeAsync(10);
    rerender({ sel: 't8' });
    await vi.advanceTimersByTimeAsync(80);

    // Only the settled selection's neighbors — no storm from intermediates.
    expect(prefetchedIds().sort()).toEqual(['t6', 't7', 't9']);
  });

  it('skips neighbors whose cache entry is still fresh', async () => {
    const items = makeItems(['t1', 't2', 't3', 't4', 't5']);
    queryClient.setQueryData(['thread', 't2'], { thread: null, messages: [], source: 'api' });

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

    expect(fetchThreadDetailMock).not.toHaveBeenCalled();
  });
});

// ---------------------------------------------------------------------------
// useNextPagePrefetch
// ---------------------------------------------------------------------------

describe('useNextPagePrefetch', () => {
  it('calls fetchNextPage exactly once per sentinel visit', () => {
    const fetchNextPage = vi.fn();
    const { rerender } = renderHook((props) => useNextPagePrefetch(props), {
      initialProps: { nearEnd: true, hasNextPage: true, isFetching: false, fetchNextPage },
    });

    expect(fetchNextPage).toHaveBeenCalledTimes(1);

    // The fetch runs, then settles with the sentinel still visible — no refire.
    rerender({ nearEnd: true, hasNextPage: true, isFetching: true, fetchNextPage });
    rerender({ nearEnd: true, hasNextPage: true, isFetching: false, fetchNextPage });

    expect(fetchNextPage).toHaveBeenCalledTimes(1);
  });

  it('fires again after the sentinel leaves and re-enters', () => {
    const fetchNextPage = vi.fn();
    const { rerender } = renderHook((props) => useNextPagePrefetch(props), {
      initialProps: { nearEnd: true, hasNextPage: true, isFetching: false, fetchNextPage },
    });
    rerender({ nearEnd: false, hasNextPage: true, isFetching: false, fetchNextPage });
    rerender({ nearEnd: true, hasNextPage: true, isFetching: false, fetchNextPage });

    expect(fetchNextPage).toHaveBeenCalledTimes(2);
  });

  it('does nothing when there is no next page, mid-fetch, far from the end, or offline', () => {
    const fetchNextPage = vi.fn();
    const { rerender } = renderHook((props) => useNextPagePrefetch(props), {
      initialProps: { nearEnd: false, hasNextPage: true, isFetching: false, fetchNextPage },
    });
    rerender({ nearEnd: true, hasNextPage: false, isFetching: false, fetchNextPage });
    rerender({ nearEnd: true, hasNextPage: true, isFetching: true, fetchNextPage });
    expect(fetchNextPage).not.toHaveBeenCalled();

    onlineState.value = false;
    rerender({ nearEnd: true, hasNextPage: true, isFetching: false, fetchNextPage });
    expect(fetchNextPage).not.toHaveBeenCalled();
  });
});
