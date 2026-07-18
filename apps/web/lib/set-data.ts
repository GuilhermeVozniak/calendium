import type { Calendar, CalendarSet, CalendarSetInput } from '@calendium/shared';

import { getApiClient } from '@/lib/api';
import { DEMO_MODE } from '@/lib/demo';
import { calendarMock } from '@/lib/calendar-mock';
import { patchCalendar } from '@/lib/calendar-data';

/**
 * Data-access wrappers for calendar sets (named groups of calendars toggled
 * together, e.g. "Work" / "Home"). They hit the real API and, in explicit
 * demo mode only (lib/demo.ts), fall back to the in-memory mock
 * (lib/calendar-mock.ts). Outside demo mode failures propagate so the UI shows
 * real loading / empty / error states and mutations surface their failures.
 */

export async function fetchCalendarSets(): Promise<CalendarSet[]> {
  try {
    return await getApiClient().listCalendarSets();
  } catch (err) {
    if (DEMO_MODE) return calendarMock.listCalendarSets();
    throw err;
  }
}

export async function createCalendarSetApi(input: CalendarSetInput): Promise<CalendarSet> {
  try {
    return await getApiClient().createCalendarSet(input);
  } catch (err) {
    if (DEMO_MODE) return calendarMock.createCalendarSet(input);
    throw err;
  }
}

export async function updateCalendarSetApi(
  id: string,
  input: CalendarSetInput
): Promise<CalendarSet> {
  try {
    return await getApiClient().updateCalendarSet(id, input);
  } catch (err) {
    if (DEMO_MODE) return calendarMock.updateCalendarSet(id, input);
    throw err;
  }
}

export async function deleteCalendarSetApi(id: string): Promise<void> {
  try {
    await getApiClient().deleteCalendarSet(id);
  } catch (err) {
    if (DEMO_MODE) {
      calendarMock.deleteCalendarSet(id);
      return;
    }
    throw err;
  }
}

// ---------------------------------------------------------------------------
// Active-set preference (client-only, per docs/architecture.md "sets apply
// via visibility" - the set membership itself is server-persisted, but which
// set is *currently active* is a per-device UI preference, not synced).
// ---------------------------------------------------------------------------

export const ACTIVE_SET_STORAGE_KEY = 'calendium.activeCalendarSet';

/** Sentinel id for the built-in "All calendars" pseudo-set (never persisted server-side). */
export const ALL_CALENDARS_SET_ID = '__all__';

/** Synthesizes the "All calendars" pseudo-set from the currently loaded calendars. */
export function allCalendarsSet(calendars: Calendar[]): CalendarSet {
  return {
    id: ALL_CALENDARS_SET_ID,
    name: 'All calendars',
    calendarIds: calendars.map((c) => c.id),
    position: -1,
  };
}

/** The currently active set id, or null. Empty outside the browser. */
export function getActiveSetId(): string | null {
  if (typeof window === 'undefined') return null;
  try {
    return window.localStorage.getItem(ACTIVE_SET_STORAGE_KEY);
  } catch {
    return null;
  }
}

/** Persist (or clear, when `id` is null) the active-set preference. */
export function setActiveSetId(id: string | null): void {
  if (typeof window === 'undefined') return;
  try {
    if (id) window.localStorage.setItem(ACTIVE_SET_STORAGE_KEY, id);
    else window.localStorage.removeItem(ACTIVE_SET_STORAGE_KEY);
  } catch {
    // localStorage unavailable (private mode, etc.) - active-set state won't
    // survive a reload, but the current session is unaffected.
  }
}

/**
 * True when every calendar's current visibility already matches what `set`
 * prescribes (a calendar is visible iff its id is in `set.calendarIds`).
 * Used to detect that a manual per-calendar toggle has diverged from the
 * active set, so its "active" badge can be cleared.
 */
export function calendarsMatchSet(calendars: Calendar[], set: CalendarSet): boolean {
  const shouldBeVisible = new Set(set.calendarIds);
  return calendars.every((c) => c.isVisible === shouldBeVisible.has(c.id));
}

/**
 * Applies a set: PATCHes `isVisible: true` for the set's calendars and
 * `isVisible: false` for every other calendar, skipping calendars already in
 * the right state (no redundant PATCHes). Visibility is the server-persisted
 * mechanism (PATCH /v1/calendars/{id}), so applying a set here is what makes
 * it "stick" across devices - the set itself only stores which calendars
 * belong to it. On success, persists `set.id` as the active-set preference
 * (see setActiveSetId) - the "All calendars" pseudo-set (allCalendarsSet)
 * flows through the same path since it's just a CalendarSet whose
 * calendarIds covers everything.
 */
export async function activateSet(set: CalendarSet, calendars: Calendar[]): Promise<void> {
  const shouldBeVisible = new Set(set.calendarIds);
  const patches = calendars
    .filter((c) => c.isVisible !== shouldBeVisible.has(c.id))
    .map((c) => patchCalendar(c.id, { isVisible: shouldBeVisible.has(c.id) }));
  await Promise.all(patches);
  setActiveSetId(set.id);
}
