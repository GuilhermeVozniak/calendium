'use client';

import type { QueryClient } from '@tanstack/react-query';

import { clearActingAs } from '@/lib/act-as';
import { invalidateAccessToken } from '@/lib/auth-client';
import { clearOfflineState } from '@/lib/offline/queue';
import { resetTourSession } from '@/lib/tour-state';

/**
 * Client side of account deletion (Track C review I-1).
 *
 * Better Auth flips its session signal the moment /delete-user succeeds, so
 * the (app) layout sees a null session and would soft-navigate to /signin,
 * racing (and usually beating) the landing on /goodbye. The dialog therefore
 * raises a module-level flag BEFORE calling deleteUser, the layout skips its
 * /signin redirect while it is up, and the dialog finishes with a hard
 * navigation. The flag needs no persistence: the hard navigation resets
 * every module, and a failed delete lowers it again.
 */

let deleting = false;

export function beginAccountDeletion(): void {
  deleting = true;
}

export function endAccountDeletion(): void {
  deleting = false;
}

export function isAccountDeletionInProgress(): boolean {
  return deleting;
}

/** localStorage keys the app owns: `calendium.*` and `calendium-*` (theme). */
const OWNED_KEY_PREFIX = 'calendium';

function removeOwnedLocalStorage(): void {
  try {
    const storage = window.localStorage;
    const owned: string[] = [];
    for (let i = 0; i < storage.length; i += 1) {
      const key = storage.key(i);
      if (key?.startsWith(OWNED_KEY_PREFIX)) owned.push(key);
    }
    for (const key of owned) storage.removeItem(key);
  } catch {
    // localStorage unavailable: nothing persisted there.
  }
}

/**
 * Browser-side only: drops the push subscription and the service worker.
 * The purge already deleted the device rows, so there is no
 * unregisterDevice call (it would hit the Go API after deletion).
 */
async function removeWebPush(): Promise<void> {
  if (typeof navigator === 'undefined' || !('serviceWorker' in navigator)) return;
  const registrations = await navigator.serviceWorker.getRegistrations();
  await Promise.all(
    registrations.map(async (reg) => {
      const sub = await reg.pushManager?.getSubscription().catch(() => null);
      await sub?.unsubscribe().catch(() => false);
      await reg.unregister().catch(() => false);
    })
  );
  if (typeof caches !== 'undefined') {
    const names = await caches.keys();
    await Promise.all(names.filter((n) => n.startsWith(OWNED_KEY_PREFIX)).map((n) => caches.delete(n)));
  }
}

async function attempt(step: () => unknown): Promise<void> {
  try {
    await step();
  } catch {
    // Best effort: a broken storage layer never blocks leaving the app.
  }
}

/**
 * After a successful delete: scrub everything the deleted user left in this
 * browser WITHOUT any network call. The cached JWT goes first (synchronously)
 * so nothing that runs afterwards can reach the API authenticated; the
 * server session is already gone, so the cache cannot mint a new one.
 */
export async function scrubLocalStateAfterDeletion(queryClient: QueryClient): Promise<void> {
  await attempt(invalidateAccessToken);
  await attempt(async () => {
    await queryClient.cancelQueries();
    queryClient.clear();
  });
  await attempt(clearActingAs);
  await attempt(resetTourSession);
  await attempt(clearOfflineState);
  await attempt(removeWebPush);
  removeOwnedLocalStorage();
}

/** Hard navigation: supersedes any pending soft navigation and drops in-memory state. */
export function leaveAfterAccountDeletion(replace: (url: string) => void = (url) => window.location.replace(url)): void {
  replace('/goodbye');
}
