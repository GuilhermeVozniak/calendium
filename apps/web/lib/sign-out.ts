'use client';

import { clearActingAs } from '@/lib/act-as';
import { signOut } from '@/lib/auth-client';
import { clearOfflineState } from '@/lib/offline/queue';
import { resetTourSession } from '@/lib/tour-state';

/**
 * The single sign-out routine. EVERY sign-out surface (user menu, command
 * palette, future ones) must go through this so no path leaves the previous
 * user's state behind on a shared device: the persisted acting-as selection
 * (M2.6 lesson — per-user state never survives the session) and the offline
 * outbox + query cache (whose queued mutations would otherwise replay as the
 * next user). Tour state is the deliberate exception: its localStorage key
 * is scoped per user (lib/tour-state.ts), so it cannot leak across users —
 * resetTourSession only detaches the in-memory scope, keeping completion so
 * re-auth on the same device doesn't re-show the tour. Navigation stays with
 * the caller — this only ends the session and scrubs local state.
 */
export async function performSignOut(): Promise<void> {
  await signOut();
  clearActingAs();
  resetTourSession();
  await clearOfflineState();
}
