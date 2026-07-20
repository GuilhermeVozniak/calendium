'use client';

import * as React from 'react';

/**
 * Client-local state for the concierge onboarding tour (M2.8 Task 19).
 *
 * Completion (and the shortcut-coach mute) persists in localStorage so the
 * tour greets a fresh user exactly once. The key is scoped PER USER
 * (`calendium.tour.v1.<userId>`), which is how the M2.6 rule — per-user
 * local state never leaks ACROSS users — is honored here: a different user
 * on the same device reads a different key and gets a fresh tour. Because
 * the key is user-scoped, sign-out deliberately does NOT remove it (see
 * resetTourSession) — re-authenticating as the same user on the same device
 * must not re-show a tour that user already completed. There is deliberately
 * no backend surface — the human 1:1 concierge session is a business process
 * out of scope; only the in-app tour + coach live here.
 */

/**
 * Pre-M2-followups releases stored tour state un-scoped under this exact
 * key. On the first read for a signed-in user with no per-user key yet, the
 * legacy value is adopted as that user's state and the legacy key removed —
 * so the device's previous (single) user doesn't get re-toured after the
 * upgrade. The one-time cost: if a DIFFERENT user signs in first on such a
 * device, they inherit the legacy completion; acceptable for a cosmetic
 * overlay, and the legacy key is gone after that single adoption.
 */
export const LEGACY_TOUR_STORAGE_KEY = 'calendium.tour.v1';

/** The per-user localStorage key for tour + coach state. */
export function tourStorageKey(userId: string): string {
  return `${LEGACY_TOUR_STORAGE_KEY}.${userId}`;
}

interface TourPersisted {
  done: boolean;
  coachMuted: boolean;
}

export interface TourSnapshot {
  active: boolean;
  stepIndex: number;
  done: boolean;
  coachMuted: boolean;
}

const DEFAULT_PERSISTED: TourPersisted = { done: false, coachMuted: false };

let currentUserId: string | null = null;
let persisted: TourPersisted | undefined;
let active = false;
let stepIndex = 0;
let snapshot: TourSnapshot | undefined;
const listeners = new Set<() => void>();

/**
 * Scopes the persisted state to the signed-in user. Called by the app shell
 * (OnboardingTour) once the session is known; until then reads return the
 * defaults and writes stay in-memory only, and maybeAutoStartTour() refuses
 * to start — a tour must never begin for an unknown user.
 */
export function setTourUser(userId: string | null): void {
  if (userId === currentUserId) return;
  currentUserId = userId;
  persisted = undefined; // re-read under the new user's key
  active = false;
  stepIndex = 0;
  emit();
}

function readPersisted(): TourPersisted {
  if (persisted === undefined) {
    if (typeof window === 'undefined' || currentUserId === null) return DEFAULT_PERSISTED;
    let loaded: TourPersisted;
    try {
      const key = tourStorageKey(currentUserId);
      let raw = window.localStorage.getItem(key);
      if (raw === null) {
        // One-time legacy adoption (see LEGACY_TOUR_STORAGE_KEY).
        const legacy = window.localStorage.getItem(LEGACY_TOUR_STORAGE_KEY);
        if (legacy !== null) {
          window.localStorage.setItem(key, legacy);
          window.localStorage.removeItem(LEGACY_TOUR_STORAGE_KEY);
          raw = legacy;
        }
      }
      loaded = raw ? { ...DEFAULT_PERSISTED, ...JSON.parse(raw) } : { ...DEFAULT_PERSISTED };
    } catch {
      loaded = { ...DEFAULT_PERSISTED };
    }
    persisted = loaded;
  }
  return persisted;
}

function writePersisted(patch: Partial<TourPersisted>): void {
  persisted = { ...readPersisted(), ...patch };
  if (currentUserId === null) return; // no user scope yet — in-memory only
  try {
    window.localStorage.setItem(tourStorageKey(currentUserId), JSON.stringify(persisted));
  } catch {
    // Persistence is best-effort; the in-memory state still applies.
  }
}

function emit(): void {
  snapshot = undefined;
  for (const listener of listeners) listener();
}

/** Whether the tour was completed (or skipped — both count as done). */
export function isTourDone(): boolean {
  return readPersisted().done;
}

export function startTour(): void {
  active = true;
  stepIndex = 0;
  emit();
}

/**
 * First-run entry point: starts the tour only for a KNOWN user who never saw
 * it. No-ops until setTourUser() has attached a session — otherwise the tour
 * could fire against the wrong (or no) user's persisted state.
 */
export function maybeAutoStartTour(): void {
  if (currentUserId !== null && !isTourDone() && !active) startTour();
}

/** Advances; finishing past the last of `totalSteps` completes the tour. */
export function nextTourStep(totalSteps: number): void {
  if (!active) return;
  if (stepIndex + 1 >= totalSteps) {
    finishTour();
    return;
  }
  stepIndex += 1;
  emit();
}

/** Skipping counts as done — a reload never re-opens a dismissed tour. */
export function skipTour(): void {
  finishTour();
}

export function finishTour(): void {
  active = false;
  stepIndex = 0;
  writePersisted({ done: true });
  emit();
}

/** Palette "Restart tour": clears completion and re-runs from step 0. */
export function restartTour(): void {
  writePersisted({ done: false });
  active = true;
  stepIndex = 0;
  emit();
}

// ---------------------------------------------------------------------------
// Shortcut coach mute (consumed by lib/shortcut-hints.ts)
// ---------------------------------------------------------------------------

export function isCoachMuted(): boolean {
  return readPersisted().coachMuted;
}

export function setCoachMuted(muted: boolean): void {
  writePersisted({ coachMuted: muted });
  emit();
}

/**
 * Sign-out MUST call this (via performSignOut): it ends the in-memory tour
 * and detaches the user scope so the next session re-reads under its own
 * key. The persisted per-user key deliberately STAYS — it is scoped to that
 * user, so it cannot leak to a different user (M2.6 rule), and keeping it is
 * what stops the tour from re-showing when the same user re-authenticates on
 * the same device.
 */
export function resetTourSession(): void {
  active = false;
  stepIndex = 0;
  currentUserId = null;
  persisted = undefined;
  emit();
}

/** Test-only: wipe module memory so the next read hits localStorage again. */
export function resetTourStateForTests(): void {
  active = false;
  stepIndex = 0;
  currentUserId = null;
  persisted = undefined;
  emit();
}

export function getTourSnapshot(): TourSnapshot {
  if (!snapshot) {
    const p = readPersisted();
    snapshot = { active, stepIndex, done: p.done, coachMuted: p.coachMuted };
  }
  return snapshot;
}

// Server render: never active (the tour is a client-only overlay).
const SERVER_SNAPSHOT: TourSnapshot = { active: false, stepIndex: 0, done: true, coachMuted: false };

export function subscribeTour(listener: () => void): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

export interface TourState {
  active: boolean;
  stepIndex: number;
  start: () => void;
  next: (totalSteps: number) => void;
  skip: () => void;
  done: () => void;
  restart: () => void;
}

/** Reactive tour state (brief interface: active/stepIndex + transitions). */
export function useTourState(): TourState {
  const snap = React.useSyncExternalStore(subscribeTour, getTourSnapshot, () => SERVER_SNAPSHOT);
  return {
    active: snap.active,
    stepIndex: snap.stepIndex,
    start: startTour,
    next: nextTourStep,
    skip: skipTour,
    done: finishTour,
    restart: restartTour,
  };
}

/** Reactive shortcut-coach mute flag (palette toggle label). */
export function useCoachMuted(): boolean {
  return React.useSyncExternalStore(
    subscribeTour,
    () => getTourSnapshot().coachMuted,
    () => false
  );
}
