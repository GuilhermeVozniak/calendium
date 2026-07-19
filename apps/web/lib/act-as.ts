'use client';

import * as React from 'react';

/**
 * Explicit acting-as state for EA delegation (M2.7 Task 15).
 *
 * The selection persists in localStorage so a reload keeps the assistant in
 * the principal's context, but it is NEVER a sticky global default: sign-out
 * MUST call clearActingAs() (M2.6 lesson — persisted per-user state never
 * survives the session), and the act-as header itself is only attached by the
 * derived ApiClient while this state is set (lib/api.ts + shared actAs).
 */
export const ACT_AS_STORAGE_KEY = 'calendium.actAs.principalId';

let current: string | null | undefined;
const listeners = new Set<() => void>();

function read(): string | null {
  if (current === undefined) {
    if (typeof window === 'undefined') return null;
    try {
      current = window.localStorage.getItem(ACT_AS_STORAGE_KEY);
    } catch {
      current = null;
    }
  }
  return current;
}

/** The principal user id currently acted for, or null when not acting. */
export function getActingAs(): string | null {
  return read();
}

/** Sets (or, with null, clears) the acting-as principal; persists best-effort. */
export function setActingAs(principalId: string | null): void {
  current = principalId;
  try {
    if (principalId === null) window.localStorage.removeItem(ACT_AS_STORAGE_KEY);
    else window.localStorage.setItem(ACT_AS_STORAGE_KEY, principalId);
  } catch {
    // Persistence is best-effort; the in-memory selection still applies.
  }
  for (const listener of listeners) listener();
}

/** Sign-out MUST call this — acting state never survives the session. */
export function clearActingAs(): void {
  setActingAs(null);
}

export function subscribeActingAs(listener: () => void): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

/** Reactive acting-as principal id (null when not acting). */
export function useActingAs(): string | null {
  return React.useSyncExternalStore(subscribeActingAs, getActingAs, () => null);
}
