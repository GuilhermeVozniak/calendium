'use client';

import {
  ApiRequestError,
  LOCAL_DRAFT_PREFIX,
  Outbox,
  createIndexedDbKv,
  createKvOutboxStorage,
  type OutboxAction,
  type OutboxEntry,
} from '@calendium/shared';
import type { QueryClient } from '@tanstack/react-query';
import { toast } from 'sonner';

import { getApiClient } from '@/lib/api';
import { isOnline, subscribeOnline } from '@/lib/offline/connectivity';

/**
 * App-level outbox wiring (M2.6 Task 4): a durable, IndexedDB-backed queue of
 * actions taken while offline, plus the replay orchestration that drains it
 * on reconnect. Honesty policy: nothing here ever reports "sent"/"synced" —
 * queued state is surfaced as queued (see components/app/outbox-indicator),
 * and only a ReplayReport from the real API clears it. Replay is
 * at-least-once: a crash mid-replay may re-send an action.
 */

let outboxInstance: Outbox | null = null;
let outboxReady: Promise<void> = Promise.resolve();

/** Lazy app singleton over the shared Outbox + IndexedDB storage. */
export function getOutbox(): Outbox {
  if (!outboxInstance) {
    outboxInstance = new Outbox(createKvOutboxStorage(createIndexedDbKv()));
    outboxReady = outboxInstance.init().catch((err) => {
      // Storage failure degrades to an empty in-memory queue — the app keeps
      // working; queued entries just won't survive a reload.
      console.warn('calendium: outbox failed to load persisted entries', err);
    });
  }
  return outboxInstance;
}

async function ready(): Promise<Outbox> {
  const outbox = getOutbox();
  await outboxReady;
  return outbox;
}

/**
 * True when the error is a transport failure (TypeError from fetch /
 * AbortError), as opposed to an ApiRequestError the server actually returned.
 */
export function isNetworkError(err: unknown): boolean {
  if (err instanceof ApiRequestError) return false;
  if (err instanceof TypeError) return true;
  // DOMException does not extend Error in every runtime (e.g. jsdom), so
  // match aborts by name on any error-shaped object.
  return (
    typeof err === 'object' &&
    err !== null &&
    (err as { name?: unknown }).name === 'AbortError'
  );
}

/**
 * Durably enqueue an offline action and tell the user the truth about it —
 * "queued", not "done". `silent` skips the toast for low-stakes internals
 * (read receipts, compose's own paired save+send, which toast once itself).
 */
export async function queueAction(
  action: OutboxAction,
  opts?: { silent?: boolean }
): Promise<void> {
  const outbox = await ready();
  await outbox.enqueue(action);
  if (!opts?.silent) toast('Offline — action queued');
}

/** Client-generated id for drafts created offline; replay swaps in the server id. */
export function newLocalDraftId(): string {
  return `${LOCAL_DRAFT_PREFIX}${crypto.randomUUID()}`;
}

function describeConflict(entry: OutboxEntry): string {
  const why = entry.lastError ? ` (${entry.lastError})` : '';
  switch (entry.action.kind) {
    case 'thread_action':
      return `A queued "${entry.action.action.replace(/_/g, ' ')}" was rejected by the server${why}.`;
    case 'thread_snooze':
      return `A queued snooze was rejected by the server${why}.`;
    case 'thread_reminder':
      return `A queued reminder was rejected by the server${why}.`;
    case 'thread_open':
      return `A queued read receipt was rejected by the server${why}.`;
    case 'draft_save':
      return `A queued draft could not be saved${why}.`;
    case 'draft_send':
      return `A queued message could NOT be sent${why}.`;
  }
}

const REPLAY_INTERVAL_MS = 30_000;

/**
 * Wires the replay triggers: the offline→online transition, window focus,
 * and a 30s safety interval. After each replay attempt the thread/draft
 * queries are invalidated so the UI reconciles with what the server actually
 * accepted, and every conflict is surfaced as a toast — except `thread_open`
 * conflicts, which are dismissed silently (a rejected read receipt is not
 * worth interrupting the user for). Returns a cleanup function.
 */
export function startOutboxReplay(queryClient: QueryClient): () => void {
  let disposed = false;
  let running = false;

  const run = async (): Promise<void> => {
    if (running || disposed || !isOnline()) return;
    running = true;
    try {
      const outbox = await ready();
      if (outbox.queuedCount === 0) return;
      const report = await outbox.replay(getApiClient());
      if (report.replayed.length > 0 || report.conflicts.length > 0) {
        void queryClient.invalidateQueries({ queryKey: ['threads'] });
        void queryClient.invalidateQueries({ queryKey: ['thread'] });
        void queryClient.invalidateQueries({ queryKey: ['drafts'] });
      }
      for (const entry of report.conflicts) {
        if (entry.action.kind === 'thread_open') {
          void outbox.dismiss(entry.id);
          continue;
        }
        toast.error(describeConflict(entry));
      }
    } finally {
      running = false;
    }
  };

  const unsubscribe = subscribeOnline((online) => {
    if (online) void run();
  });
  const onFocus = () => void run();
  window.addEventListener('focus', onFocus);
  const interval = setInterval(() => void run(), REPLAY_INTERVAL_MS);
  void run(); // catch up immediately when mounted already-online with a backlog

  return () => {
    disposed = true;
    unsubscribe();
    window.removeEventListener('focus', onFocus);
    clearInterval(interval);
  };
}
