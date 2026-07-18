import type {
  AvailabilitySlot,
  Calendar,
  CalendarSet,
  Event,
  EventInput,
  EventPatch,
  RsvpStatus,
} from '@calendium/shared';

import { getApiClient } from '@/lib/api';
import { DEMO_MODE } from '@/lib/demo';
import { calendarMock } from '@/lib/calendar-mock';

/**
 * Data-access wrappers for the calendar UI. They hit the real API and, in
 * explicit demo mode only (lib/demo.ts), fall back to the in-memory mock
 * (lib/calendar-mock.ts). Outside demo mode failures propagate so the UI shows
 * real loading / empty / error states and mutations surface their failures.
 */

export async function fetchCalendars(): Promise<Calendar[]> {
  try {
    return await getApiClient().listCalendars();
  } catch (err) {
    if (DEMO_MODE) return calendarMock.listCalendars();
    throw err;
  }
}

export async function patchCalendar(
  id: string,
  patch: Partial<Pick<Calendar, 'isVisible' | 'color'>>
): Promise<Calendar> {
  try {
    return await getApiClient().updateCalendar(id, patch);
  } catch (err) {
    if (DEMO_MODE) return calendarMock.updateCalendar(id, patch);
    throw err;
  }
}

export async function fetchEvents(from: Date, to: Date): Promise<Event[]> {
  try {
    return await getApiClient().listEvents(from.toISOString(), to.toISOString());
  } catch (err) {
    if (DEMO_MODE) return calendarMock.listEvents(from.toISOString(), to.toISOString());
    throw err;
  }
}

/**
 * Unfiltered event fetch for cross-account conflict math (Task 12): the same
 * range query as fetchEvents, but deliberately never narrowed to visible
 * calendars, so hidden calendars and other connected accounts still count as
 * busy time. Consumed by double-booking warnings and quick-add slot
 * suggestions - fetchEvents itself already happens not to filter (visibility
 * is applied client-side by callers), but this wrapper keeps the "unfiltered"
 * guarantee explicit and independent of whatever fetchEvents does next.
 */
export async function fetchBusyEvents(from: Date, to: Date): Promise<Event[]> {
  try {
    return await getApiClient().listEvents(from.toISOString(), to.toISOString());
  } catch (err) {
    if (DEMO_MODE) return calendarMock.listEvents(from.toISOString(), to.toISOString());
    throw err;
  }
}

export async function createEventApi(input: EventInput): Promise<Event> {
  try {
    return await getApiClient().createEvent(input);
  } catch (err) {
    if (DEMO_MODE) return calendarMock.createEvent(input);
    throw err;
  }
}

export async function updateEventApi(id: string, patch: EventPatch): Promise<Event> {
  try {
    return await getApiClient().updateEvent(id, patch);
  } catch (err) {
    if (DEMO_MODE) return calendarMock.updateEvent(id, patch);
    throw err;
  }
}

export async function deleteEventApi(id: string): Promise<void> {
  try {
    await getApiClient().deleteEvent(id);
  } catch (err) {
    if (DEMO_MODE) {
      calendarMock.deleteEvent(id);
      return;
    }
    throw err;
  }
}

export async function sendRsvpApi(id: string, response: RsvpStatus): Promise<Event> {
  try {
    return await getApiClient().rsvp(id, response);
  } catch (err) {
    if (DEMO_MODE) return calendarMock.rsvp(id, response);
    throw err;
  }
}

/**
 * Named calendar sets ("Work", "Home") toggled together from the command
 * palette's "Calendar set: <name>" entries (Task 15). No demo-mode fallback
 * exists yet for this resource, so demo mode simply reports no sets rather
 * than throwing - the palette then lists none, which is a safe default.
 */
export async function fetchCalendarSets(): Promise<CalendarSet[]> {
  try {
    return await getApiClient().listCalendarSets();
  } catch (err) {
    if (DEMO_MODE) return [];
    throw err;
  }
}

export async function fetchAvailability(
  from: Date,
  to: Date,
  durationMinutes: number
): Promise<AvailabilitySlot[]> {
  try {
    return await getApiClient().getAvailability(from.toISOString(), to.toISOString(), durationMinutes);
  } catch (err) {
    if (DEMO_MODE) return calendarMock.availability(from.toISOString(), to.toISOString(), durationMinutes);
    throw err;
  }
}
