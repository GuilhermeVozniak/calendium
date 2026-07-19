import { api } from '@/lib/api';
import {
  ApiRequestError,
  OUTBOX_STORAGE_KEY,
  Outbox,
  createKvOutboxStorage,
  type OutboxAction,
} from '@calendium/shared';
import AsyncStorage from '@react-native-async-storage/async-storage';
import type { QueryClient } from '@tanstack/react-query';
import * as Network from 'expo-network';
import * as React from 'react';
import { Alert, AppState } from 'react-native';

/**
 * Offline outbox wiring for mobile: triage actions that fail with a NETWORK
 * error (server unreachable) are queued durably in AsyncStorage and replayed
 * at-least-once when reachability returns or the app foregrounds. Server-side
 * rejections (ApiRequestError) are never queued — those are real answers and
 * must surface honestly, not be retried into fake success.
 */

/**
 * RN-safe id source injected into the shared Outbox: Hermes does not
 * guarantee crypto.randomUUID, so fall back to a time+random token. Ids only
 * ever identify entries within this install's own queue, so device-local
 * uniqueness is all that's required.
 */
export function generateOutboxId(): string {
  const cryptoObj = globalThis.crypto as { randomUUID?: () => string } | undefined;
  if (typeof cryptoObj?.randomUUID === 'function') return cryptoObj.randomUUID();
  const rand = () => Math.random().toString(36).slice(2, 10);
  return `ob_${Date.now().toString(36)}_${rand()}${rand()}`;
}

let outbox: Outbox | null = null;
let ready: Promise<void> | null = null;

/** App-wide outbox singleton, durably backed by AsyncStorage ('outbox:v1'). */
export function getOutbox(): Outbox {
  if (!outbox) {
    outbox = new Outbox(createKvOutboxStorage(AsyncStorage), generateOutboxId);
    ready = outbox.init();
  }
  return outbox;
}

/** Resolves once entries persisted by previous sessions have been loaded. */
export function outboxReady(): Promise<void> {
  getOutbox();
  return ready as Promise<void>;
}

/** Test-only: drops the singleton so each test starts from clean storage. */
export function resetOutboxForTests(): void {
  outbox = null;
  ready = null;
}

/**
 * Sign-out hygiene: empty the in-memory outbox (so a replay trigger racing
 * sign-out has nothing left to send for the previous user) and remove its
 * persisted AsyncStorage key. Best-effort by design — a broken storage layer
 * must never block sign-out.
 */
export async function clearOfflineState(): Promise<void> {
  try {
    if (outbox) {
      await outboxReady();
      await outbox.clear();
    }
    await AsyncStorage.removeItem(OUTBOX_STORAGE_KEY);
  } catch (err) {
    console.warn('calendium: failed to clear offline outbox on sign-out', err);
  }
}

/**
 * True when a request failed to REACH the server (fetch TypeError, timeout,
 * dropped connection). An ApiRequestError means the server answered — paywall,
 * validation, 5xx — and is never treated as "offline".
 */
export function isNetworkError(err: unknown): boolean {
  if (err instanceof ApiRequestError) return false;
  if (err instanceof TypeError) return true; // fetch's connectivity failure
  return err instanceof Error && /network|timeout|abort|connection|unreachable/i.test(err.message);
}

/**
 * Queues `action` iff `error` is a connectivity failure. Returns true when
 * queued — the caller should then KEEP its optimistic UI as an honest pending
 * change (surfaced by the queued badge) instead of rolling back or alerting.
 */
export async function queueIfOffline(error: unknown, action: OutboxAction): Promise<boolean> {
  if (!isNetworkError(error)) return false;
  await outboxReady();
  await getOutbox().enqueue(action);
  return true;
}

const isReachable = (state: { isConnected?: boolean; isInternetReachable?: boolean | null }) =>
  state.isConnected === true && state.isInternetReachable !== false;

/**
 * Starts the replay triggers: expo-network reachability changes, AppState
 * returning to 'active', and one initial reachability-gated kick to catch up
 * on entries queued in a previous session. Returns a cleanup that removes
 * both subscriptions. Replay is at-least-once and concurrency-safe (the
 * shared Outbox refuses overlapping replays); after anything actually
 * replays — or is rejected as a conflict — mail queries are invalidated so
 * lists reflect the server's truth, and conflicts surface a notice instead of
 * silently clearing the queued badge. Conflict entries stay in the outbox
 * (status 'conflict') until explicitly dismissed.
 */
export function startOutboxReplay(queryClient: QueryClient): () => void {
  const box = getOutbox();
  let disposed = false;

  const replayNow = async () => {
    await outboxReady();
    if (disposed) return;
    const report = await box.replay(api);
    // A conflict is the server's real answer to a queued change, so even with
    // replayed=0 the optimistic state it backed is now stale — refetch to
    // server truth in that case too.
    if (report.replayed.length > 0 || report.conflicts.length > 0) {
      await queryClient.invalidateQueries({ queryKey: ['threads'] });
      await queryClient.invalidateQueries({ queryKey: ['thread'] });
    }
    if (report.conflicts.length > 0) {
      // Never let a cleared badge read as success: say what didn't apply.
      // Entries remain in the outbox as 'conflict' until dismissed.
      const n = report.conflicts.length;
      Alert.alert(
        'Offline changes rejected',
        `${n} queued change${n === 1 ? ' was' : 's were'} rejected by the server and not applied.`
      );
    }
  };

  const replayIfReachable = async () => {
    try {
      const state = await Network.getNetworkStateAsync();
      if (!disposed && isReachable(state)) await replayNow();
    } catch {
      // Reachability probe failed — entries stay queued; a later trigger retries.
    }
  };

  const networkSub = Network.addNetworkStateListener((state) => {
    if (isReachable(state)) void replayNow();
  });
  const appStateSub = AppState.addEventListener('change', (status) => {
    if (status === 'active') void replayIfReachable();
  });
  void replayIfReachable();

  return () => {
    disposed = true;
    networkSub.remove();
    // Optional-chained: RN's AppState mock (and older RN APIs) can hand back
    // undefined instead of a subscription under test environments.
    appStateSub?.remove();
  };
}

/** Live count of queued (not yet replayed) offline actions, for pending badges. */
export function useQueuedCount(): number {
  const box = getOutbox();
  const subscribe = React.useCallback((onChange: () => void) => box.subscribe(onChange), [box]);
  return React.useSyncExternalStore(
    subscribe,
    () => box.queuedCount,
    () => 0
  );
}
