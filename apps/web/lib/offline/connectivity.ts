'use client';

import * as React from 'react';

/**
 * Real connectivity tracker backing offline queueing (M2.6). Two signals fold
 * into one boolean:
 *  - the browser's own `navigator.onLine`, seeded at module load and then
 *    driven by window 'online'/'offline' events, and
 *  - Calendium API reachability, reported by the `useApiOnline` probe in
 *    lib/use-mail.ts via `reportApiReachable`.
 * Browser online + API down still counts as offline for queueing purposes — a
 * request that cannot reach the server is offline no matter what the OS says.
 *
 * DEMO_MODE note: the probe layer deliberately does NOT fold reachability in
 * while demo mode is on (the API is unreachable there by design, and demo data
 * is served locally), so demo connectivity is the browser signal alone.
 */

let browserOnline = typeof navigator === 'undefined' ? true : navigator.onLine;
let apiReachable = true;

const listeners = new Set<(online: boolean) => void>();

export function isOnline(): boolean {
  return browserOnline && apiReachable;
}

/** Applies a state change and notifies subscribers only when the EFFECTIVE value flipped. */
function update(mutate: () => void): void {
  const before = isOnline();
  mutate();
  const after = isOnline();
  if (before === after) return;
  for (const fn of [...listeners]) fn(after);
}

/** Called by the API-probe layer to fold server reachability in. */
export function reportApiReachable(ok: boolean): void {
  update(() => {
    apiReachable = ok;
  });
}

/** Subscribe to effective-connectivity changes; fires once per change. */
export function subscribeOnline(fn: (online: boolean) => void): () => void {
  listeners.add(fn);
  return () => {
    listeners.delete(fn);
  };
}

if (typeof window !== 'undefined') {
  window.addEventListener('online', () =>
    update(() => {
      browserOnline = true;
    })
  );
  window.addEventListener('offline', () =>
    update(() => {
      browserOnline = false;
    })
  );
}

/** React view over the tracker. Server snapshot is `true` (no offline flash during SSR). */
export function useOnline(): boolean {
  return React.useSyncExternalStore(
    (onStoreChange) => subscribeOnline(onStoreChange),
    isOnline,
    () => true
  );
}
