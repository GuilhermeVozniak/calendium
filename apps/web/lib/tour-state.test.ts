import { beforeEach, describe, expect, it } from 'vitest';

import {
  TOUR_STORAGE_KEY,
  clearTourState,
  getTourSnapshot,
  isCoachMuted,
  isTourDone,
  maybeAutoStartTour,
  nextTourStep,
  resetTourStateForTests,
  restartTour,
  setCoachMuted,
  skipTour,
  startTour,
} from './tour-state';

describe('tour-state', () => {
  beforeEach(() => {
    window.localStorage.clear();
    resetTourStateForTests();
  });

  it('fresh user is auto-eligible and starts at step 0', () => {
    expect(isTourDone()).toBe(false);
    maybeAutoStartTour();
    expect(getTourSnapshot()).toMatchObject({ active: true, stepIndex: 0 });
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

  it('completion persists across reload and blocks auto-start', () => {
    startTour();
    skipTour();
    // Simulate a reload: wipe module memory, keep localStorage.
    resetTourStateForTests();
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
    expect(isCoachMuted()).toBe(true);
  });

  it('clearTourState wipes the persisted key (sign-out contract)', () => {
    startTour();
    skipTour();
    setCoachMuted(true);
    clearTourState();
    expect(window.localStorage.getItem(TOUR_STORAGE_KEY)).toBeNull();
    expect(isTourDone()).toBe(false);
    expect(isCoachMuted()).toBe(false);
    expect(getTourSnapshot().active).toBe(false);
  });
});
