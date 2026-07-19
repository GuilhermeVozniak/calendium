'use client';

import { clearActingAs } from '@/lib/act-as';
import { signOut } from '@/lib/auth-client';
import { clearOfflineState } from '@/lib/offline/queue';

/**
 * The single sign-out routine. EVERY sign-out surface (user menu, command
 * palette, future ones) must go through this so no path leaves the previous
 * user's state behind on a shared device: the persisted acting-as selection
 * (M2.6 lesson — per-user state never survives the session) and the offline
 * outbox + query cache (whose queued mutations would otherwise replay as the
 * next user). Navigation stays with the caller — this only ends the session
 * and scrubs local state.
 */
export async function performSignOut(): Promise<void> {
  await signOut();
  clearActingAs();
  await clearOfflineState();
}
