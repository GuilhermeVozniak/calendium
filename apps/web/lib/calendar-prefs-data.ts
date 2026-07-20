import type { CalendarPrefs, CalendarPrefsPatch } from '@calendium/shared';

import { getApiClient } from '@/lib/api';
import { DEMO_MODE } from '@/lib/demo';

/**
 * Data-access wrappers for calendar automation preferences (M2.8 Task 5,
 * GET/PATCH /v1/prefs/calendar). scheduling-data.ts pattern: real API first;
 * in explicit demo mode only, fall back to a localStorage-backed document so
 * the section stays usable in demos. Outside demo mode failures propagate —
 * including the 400 validation and 402 paywall responses callers check via
 * ApiRequestError.
 */

/** Client-side mirror of backend DefaultCalendarPrefs: UTC, Mon–Fri 9–5, all automations off. */
export const DEFAULT_CALENDAR_PREFS: CalendarPrefs = {
  timeZone: 'UTC',
  workDays: [1, 2, 3, 4, 5],
  workdayStartMinutes: 9 * 60,
  workdayEndMinutes: 17 * 60,
  focusGoalMinutesPerWeek: 0,
  focusAutoDecline: false,
  focusDeclineMessage: '',
  autoBufferMinutes: 0,
  oooAutoDecline: false,
  oooDeclineMessage: '',
  travelBuffers: false,
  travelMode: 'driving',
  leaveAlerts: false,
  homeLat: null,
  homeLon: null,
  weatherEnabled: false,
};

const DEMO_KEY = 'calendium.demo-calendar-prefs';

function getDemoCalendarPrefs(): CalendarPrefs {
  if (typeof window === 'undefined') return DEFAULT_CALENDAR_PREFS;
  const stored = window.localStorage.getItem(DEMO_KEY);
  if (!stored) return DEFAULT_CALENDAR_PREFS;
  try {
    return { ...DEFAULT_CALENDAR_PREFS, ...JSON.parse(stored) };
  } catch {
    return DEFAULT_CALENDAR_PREFS;
  }
}

function setDemoCalendarPrefs(prefs: CalendarPrefs): void {
  if (typeof window === 'undefined') return;
  window.localStorage.setItem(DEMO_KEY, JSON.stringify(prefs));
}

export async function fetchCalendarPrefs(): Promise<CalendarPrefs> {
  try {
    return await getApiClient().getCalendarPrefs();
  } catch (err) {
    if (DEMO_MODE) return getDemoCalendarPrefs();
    throw err;
  }
}

export async function updateCalendarPrefsApi(patch: CalendarPrefsPatch): Promise<CalendarPrefs> {
  try {
    return await getApiClient().updateCalendarPrefs(patch);
  } catch (err) {
    if (DEMO_MODE) {
      const next = { ...getDemoCalendarPrefs(), ...patch } as CalendarPrefs;
      setDemoCalendarPrefs(next);
      return next;
    }
    throw err;
  }
}
