// Offline support for the Wails frontend (M2.6): connectivity classification,
// the durable outbox (shared core over IndexedDB — the Wails webview
// WKWebView/WebView2 fully supports it), and replay-on-reconnect.
//
// Honesty policy: the queued pill reflects the REAL persisted outbox state,
// replay is at-least-once (entries stay queued until the server confirms),
// and conflicts surface + roll back to server truth — never fabricated sync.
import type { OutboxAction, OutboxEntry } from '@calendium/shared';
import {
  ApiRequestError,
  LOCAL_DRAFT_PREFIX,
  Outbox,
  createIndexedDbKv,
  createKvOutboxStorage,
} from '@calendium/shared';
import type { QueryClient } from '@tanstack/react-query';
import { useSyncExternalStore } from 'react';

import { api, apiConfigured } from './api';
import { isDemoMode } from './server-config';
import { toast } from './toast';

// ---------------------------------------------------------------------------
// Connectivity
// ---------------------------------------------------------------------------

export function isOnline(): boolean {
  return typeof navigator === 'undefined' || navigator.onLine;
}

/**
 * True only for fetch-level failures where the request never got a server
 * response (the queue-and-replay-later case). An ApiRequestError means the
 * server DID answer — queueing a retry would not change its mind, so those
 * always surface to the user instead.
 */
export function isNetworkError(err: unknown): boolean {
  if (err instanceof ApiRequestError) return false;
  if (err instanceof TypeError) return true; // fetch() network failure in every engine
  return err instanceof Error && /network|failed to fetch|load failed/i.test(err.message);
}

/** Client-side id for a draft created while offline; replay swaps in the server id. */
export function newLocalDraftId(): string {
  return `${LOCAL_DRAFT_PREFIX}${crypto.randomUUID()}`;
}

// ---------------------------------------------------------------------------
// Outbox singleton
// ---------------------------------------------------------------------------

let initPromise: Promise<Outbox> | null = null;
let queuedCount = 0;
const countListeners = new Set<() => void>();

export function getOutbox(): Promise<Outbox> {
  if (!initPromise) {
    const outbox = new Outbox(createKvOutboxStorage(createIndexedDbKv()));
    outbox.subscribe((entries: readonly OutboxEntry[]) => {
      // subscribe() hands us the outbox's live array — read it, never mutate.
      const next = entries.filter((e) => e.status === 'queued').length;
      if (next === queuedCount) return;
      queuedCount = next;
      for (const listener of countListeners) listener();
    });
    initPromise = outbox
      .init()
      .then(() => outbox)
      .catch((err: unknown) => {
        initPromise = null; // storage may recover; allow a retry
        throw err;
      });
  }
  return initPromise;
}

/**
 * Durably queue an action for replay. Returns false when persistence itself
 * failed (KV save errors propagate by design) — the caller must then revert
 * its optimistic state and surface the error rather than pretend the action
 * is safely queued.
 */
export async function queueOffline(action: OutboxAction): Promise<boolean> {
  try {
    const outbox = await getOutbox();
    await outbox.enqueue(action);
    return true;
  } catch {
    return false;
  }
}

function subscribeQueuedCount(listener: () => void): () => void {
  countListeners.add(listener);
  void getOutbox().catch(() => {}); // kick off init so the count is real, not assumed 0
  return () => {
    countListeners.delete(listener);
  };
}

/** Live count of actions genuinely persisted and awaiting replay (the pill). */
export function useQueuedCount(): number {
  return useSyncExternalStore(
    subscribeQueuedCount,
    () => queuedCount,
    () => 0
  );
}

// ---------------------------------------------------------------------------
// Replay
// ---------------------------------------------------------------------------

async function replayNow(queryClient: QueryClient): Promise<void> {
  // No configured server (or demo mode) = nothing truthful to sync against.
  if (isDemoMode() || !apiConfigured()) return;
  let outbox: Outbox;
  try {
    outbox = await getOutbox();
  } catch {
    return; // storage unavailable — nothing was queued to replay
  }
  const report = await outbox.replay(api);

  // Unrecoverable entries: tell the user the truth, then clear them —
  // invalidation below rolls the UI back to what the server actually holds.
  for (const entry of [...report.conflicts, ...report.failed]) {
    toast({
      title: 'An offline change could not be synced',
      description: entry.lastError ?? 'The server rejected it. Your view was refreshed.',
      variant: 'destructive',
    });
    await outbox.dismiss(entry.id);
  }

  // Only refetch when the server state actually moved (or diverged from the
  // optimistic UI). An interrupted replay changed nothing — stay as-is.
  if (report.replayed.length > 0 || report.conflicts.length > 0 || report.failed.length > 0) {
    await queryClient.invalidateQueries();
  }
}

/**
 * Wire replay-on-reconnect: flushes anything left from a previous session
 * immediately (when online) and re-runs on every browser `online` event.
 * Returns a cleanup that unhooks the listener.
 */
export function startOutboxReplay(queryClient: QueryClient): () => void {
  const run = () => void replayNow(queryClient).catch(() => {});
  window.addEventListener('online', run);
  if (isOnline()) run();
  return () => window.removeEventListener('online', run);
}
