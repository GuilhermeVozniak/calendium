'use client';

import { useQueryClient, type QueryClient } from '@tanstack/react-query';
import * as React from 'react';

import { isOnline } from '@/lib/offline/connectivity';
import { fetchThreadDetail } from '@/lib/use-mail';

/**
 * Preloading (M2.6, Task 7): hover-intent, neighbor, and next-page prefetch
 * for the mail list. All of it is silent and best-effort — prefetchQuery
 * swallows errors, so a failed prefetch never surfaces loading UI or an error
 * toast; the real fetch on open still reports honestly. Nothing here fires
 * while the connectivity tracker says we're offline: a prefetch that cannot
 * reach the server is a wasted (and misleading) request.
 */

/** Debounce for hover intent and selection settle — one j/k repeat interval. */
const PREFETCH_DELAY_MS = 80;

/**
 * How long a prefetched (or just-viewed) thread body counts as fresh enough to
 * skip re-prefetching. This only gates PREFETCHES — useThreadDetail keeps its
 * own staleness rules, so opening a thread still revalidates in the background.
 */
const PREFETCH_STALE_TIME_MS = 30_000;

/**
 * Warms ['thread', id] with the exact key + queryFn useThreadDetail uses, so a
 * later open is a cache hit, never a duplicate entry. Fresh entries (within
 * PREFETCH_STALE_TIME_MS) are skipped by prefetchQuery itself.
 */
function prefetchThread(queryClient: QueryClient, threadId: string): void {
  void queryClient.prefetchQuery({
    queryKey: ['thread', threadId],
    queryFn: () => fetchThreadDetail(threadId),
    staleTime: PREFETCH_STALE_TIME_MS,
  });
}

/**
 * Hover-intent prefetch of a thread body: fires after `delayMs` (default 80)
 * of sustained hover on one row. Returns handlers for the row to wire into
 * onMouseEnter/onMouseLeave; moving between rows restarts the timer, so a
 * mouse sweep across the list prefetches nothing.
 */
export function useThreadHoverPrefetch(delayMs: number = PREFETCH_DELAY_MS): {
  onHoverStart: (threadId: string) => void;
  onHoverEnd: () => void;
} {
  const queryClient = useQueryClient();
  const timerRef = React.useRef<ReturnType<typeof setTimeout> | null>(null);

  const onHoverEnd = React.useCallback(() => {
    if (timerRef.current !== null) {
      clearTimeout(timerRef.current);
      timerRef.current = null;
    }
  }, []);

  const onHoverStart = React.useCallback(
    (threadId: string) => {
      onHoverEnd();
      timerRef.current = setTimeout(() => {
        timerRef.current = null;
        if (!isOnline()) return;
        prefetchThread(queryClient, threadId);
      }, delayMs);
    },
    [queryClient, delayMs, onHoverEnd]
  );

  // Never leave a timer running past unmount.
  React.useEffect(() => onHoverEnd, [onHoverEnd]);

  return { onHoverStart, onHoverEnd };
}

/**
 * On selection change, prefetch the j/k targets: idx±1 first (most likely
 * next), then idx±2. Debounced by `delayMs` so holding j/k sweeps the cursor
 * without firing a prefetch per intermediate row — only the settled
 * selection's neighbors are fetched.
 */
export function usePrefetchNeighbors(
  items: ReadonlyArray<{ id: string }>,
  selectedId: string | null,
  delayMs: number = PREFETCH_DELAY_MS
): void {
  const queryClient = useQueryClient();
  React.useEffect(() => {
    if (!selectedId) return;
    const timer = setTimeout(() => {
      if (!isOnline()) return;
      const idx = items.findIndex((t) => t.id === selectedId);
      if (idx === -1) return;
      for (const offset of [1, -1, 2, -2]) {
        const neighbor = items[idx + offset];
        if (neighbor) prefetchThread(queryClient, neighbor.id);
      }
    }, delayMs);
    return () => clearTimeout(timer);
  }, [items, selectedId, delayMs, queryClient]);
}

/**
 * Calls fetchNextPage() when the scroll sentinel (10th-from-last row) becomes
 * visible and a next page exists. Fires once per sentinel visit — the guard
 * resets only when the sentinel leaves the viewport (nearEnd falls back to
 * false, e.g. because the appended page pushed it away) — so a slow response
 * can't be requested twice.
 */
export function useNextPagePrefetch(opts: {
  nearEnd: boolean;
  hasNextPage: boolean;
  isFetching: boolean;
  fetchNextPage: () => void;
}): void {
  const { nearEnd, hasNextPage, isFetching, fetchNextPage } = opts;
  const firedRef = React.useRef(false);
  React.useEffect(() => {
    if (!nearEnd) {
      firedRef.current = false;
      return;
    }
    if (firedRef.current || !hasNextPage || isFetching || !isOnline()) return;
    firedRef.current = true;
    fetchNextPage();
  }, [nearEnd, hasNextPage, isFetching, fetchNextPage]);
}
