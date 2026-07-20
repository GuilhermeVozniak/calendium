import { beforeEach, describe, expect, it } from 'vitest';

import {
  LEGACY_TOUR_STORAGE_KEY,
  getTourSnapshot,
  isCoachMuted,
  isTourDone,
  maybeAutoStartTour,
  nextTourStep,
  resetTourSession,
  resetTourStateForTests,
  restartTour,
  setCoachMuted,
  setTourUser,
  skipTour,
  startTour,
  tourStorageKey,
} from './tour-state';

describe('tour-state', () => {
  beforeEach(() => {
    window.localStorage.clear();
    resetTourStateForTests();
    setTourUser('u1');
  });

  it('fresh user is auto-eligible and starts at step 0', () => {
    expect(isTourDone()).toBe(false);
    maybeAutoStartTour();
    expect(getTourSnapshot()).toMatchObject({ active: true, stepIndex: 0 });
  });

  it('never auto-starts before a user scope is attached', () => {
    resetTourStateForTests(); // detaches the user
    maybeAutoStartTour();
    expect(getTourSnapshot().active).toBe(false);
  });

  it('next advances and finishes past the last step', () => {
    startTour();
    nextTourStep(3);
    expect(getTourSnapshot().stepIndex).toBe(1);
    nextTourStep(3);
    expect(getTourSnapshot().stepIndex).toBe(2);
    nextTourStep(3);
    expect(getTourSnapshot().active).toBe(false);
    expect(isTourDone()).toBe(true);
  });

  it('completion persists under the per-user key across reload and blocks auto-start', () => {
    startTour();
    skipTour();
    expect(window.localStorage.getItem(tourStorageKey('u1'))).toContain('"done":true');
    // Simulate a reload: wipe module memory, keep localStorage.
    resetTourStateForTests();
    setTourUser('u1');
    expect(isTourDone()).toBe(true);
    maybeAutoStartTour();
    expect(getTourSnapshot().active).toBe(false);
  });

  it('restart clears completion and re-activates at step 0', () => {
    startTour();
    skipTour();
    restartTour();
    expect(getTourSnapshot()).toMatchObject({ active: true, stepIndex: 0 });
    expect(isTourDone()).toBe(false);
  });

  it('coach mute persists across reload', () => {
    setCoachMuted(true);
    resetTourStateForTests();
    setTourUser('u1');
    expect(isCoachMuted()).toBe(true);
  });

  it('a different user on the same device gets a fresh tour (M2.6 rule)', () => {
    startTour();
    skipTour();
    setCoachMuted(true);
    setTourUser('u2');
    expect(isTourDone()).toBe(false);
    expect(isCoachMuted()).toBe(false);
    maybeAutoStartTour();
    expect(getTourSnapshot().active).toBe(true);
    // u2's completion lands under u2's key; u1's stays untouched.
    skipTour();
    expect(window.localStorage.getItem(tourStorageKey('u2'))).toContain('"done":true');
    expect(window.localStorage.getItem(tourStorageKey('u1'))).toContain('"done":true');
  });

  it('sign-out detaches the scope but keeps the per-user key (re-auth stays toured)', () => {
    startTour();
    skipTour();
    resetTourSession();
    // Detached: nothing active, and no user to auto-start for.
    expect(getTourSnapshot().active).toBe(false);
    maybeAutoStartTour();
    expect(getTourSnapshot().active).toBe(false);
    // The same user signing back in on this device is still done.
    expect(window.localStorage.getItem(tourStorageKey('u1'))).toContain('"done":true');
    setTourUser('u1');
    expect(isTourDone()).toBe(true);
    maybeAutoStartTour();
    expect(getTourSnapshot().active).toBe(false);
  });

  it('adopts the legacy un-scoped key once, then removes it', () => {
    window.localStorage.clear();
    resetTourStateForTests();
    window.localStorage.setItem(
      LEGACY_TOUR_STORAGE_KEY,
      JSON.stringify({ done: true, coachMuted: true })
    );
    setTourUser('u1');
    expect(isTourDone()).toBe(true);
    expect(isCoachMuted()).toBe(true);
    expect(window.localStorage.getItem(LEGACY_TOUR_STORAGE_KEY)).toBeNull();
    expect(window.localStorage.getItem(tourStorageKey('u1'))).toContain('"done":true');
    // A later, different user does NOT inherit it — the legacy key is gone.
    setTourUser('u2');
    expect(isTourDone()).toBe(false);
  });
});
