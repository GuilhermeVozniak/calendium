import { useQueryClient, type QueryClient } from '@tanstack/react-query';
import * as React from 'react';

import { api, orMock } from '@/lib/api';
import { mockThread } from '@/lib/mock';
import { isOnline } from '@/lib/offline';
import { isDemoMode } from '@/lib/server-config';

/**
 * Preloading (M2.6, Task 7): hover-intent and j/k-neighbor prefetch for the
 * inbox list. Silent and best-effort — prefetchQuery swallows errors, so a
 * failed prefetch never surfaces a spinner or an error toast; ThreadPane's own
 * query still reports honestly when the thread is actually opened. No network
 * prefetch fires while offline (demo mode is exempt: its data is local).
 *
 * The desktop list is non-paginated today, so next-page prefetch is web-only
 * until it paginates (see apps/web/lib/use-prefetch.ts).
 */

/** Debounce for hover intent and cursor settle — one j/k repeat interval. */
const PREFETCH_DELAY_MS = 80;

/** Prefetched thread bodies count as fresh this long — re-prefetches are skipped. */
const PREFETCH_STALE_TIME_MS = 30_000;

/**
 * Same fetch ThreadPane's ['thread', threadId] query runs (api.getThread with
 * the demo-mode mock fallback), so a prefetch fills the exact cache entry the
 * pane reads — a hit, never a duplicate.
 */
export function fetchThreadDetail(threadId: string) {
  return orMock(
    () => api.getThread(threadId),
    () => mockThread(threadId)
  );
}

/** Demo mode serves local data — only real-mode prefetches need connectivity. */
function canPrefetch(): boolean {
  return isDemoMode() || isOnline();
}

function prefetchThread(queryClient: QueryClient, threadId: string): void {
  void queryClient.prefetchQuery({
    queryKey: ['thread', threadId],
    queryFn: () => fetchThreadDetail(threadId),
    staleTime: PREFETCH_STALE_TIME_MS,
  });
}

/**
 * Hover-intent prefetch: fires after `delayMs` (default 80) of sustained hover
 * on one row; moving between rows restarts the timer, so sweeping the mouse
 * across the list prefetches nothing.
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
        if (!canPrefetch()) return;
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
 * On selection (cursor) change, prefetch the j/k targets: idx±1 first, then
 * idx±2. Debounced by `delayMs` so holding j/k sweeps the cursor without a
 * prefetch per intermediate row.
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
      if (!canPrefetch()) return;
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
