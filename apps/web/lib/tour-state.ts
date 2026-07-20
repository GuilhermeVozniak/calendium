'use client';

import * as React from 'react';

/**
 * Client-local state for the concierge onboarding tour (M2.8 Task 19).
 *
 * Completion (and the shortcut-coach mute) persists in localStorage so the
 * tour greets a fresh user exactly once, but it is per-user device state:
 * sign-out MUST call clearTourState() (M2.6 lesson — persisted per-user state
 * never survives the session; see lib/sign-out.ts). There is deliberately no
 * backend surface — the human 1:1 concierge session is a business process out
 * of scope; only the in-app tour + coach live here.
 */
export const TOUR_STORAGE_KEY = 'calendium.tour.v1';

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

let persisted: TourPersisted | undefined;
let active = false;
let stepIndex = 0;
let snapshot: TourSnapshot | undefined;
const listeners = new Set<() => void>();

function readPersisted(): TourPersisted {
  if (persisted === undefined) {
    if (typeof window === 'undefined') return DEFAULT_PERSISTED;
    let loaded: TourPersisted;
    try {
      const raw = window.localStorage.getItem(TOUR_STORAGE_KEY);
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
  try {
    window.localStorage.setItem(TOUR_STORAGE_KEY, JSON.stringify(persisted));
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

/** First-run entry point: starts the tour only for users who never saw it. */
export function maybeAutoStartTour(): void {
  if (!isTourDone() && !active) startTour();
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

/** Sign-out MUST call this — tour/coach state never survives the session. */
export function clearTourState(): void {
  active = false;
  stepIndex = 0;
  persisted = { ...DEFAULT_PERSISTED };
  try {
    window.localStorage.removeItem(TOUR_STORAGE_KEY);
  } catch {
    // Best-effort; in-memory state is already reset.
  }
  emit();
}

/** Test-only: wipe module memory so the next read hits localStorage again. */
export function resetTourStateForTests(): void {
  active = false;
  stepIndex = 0;
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
