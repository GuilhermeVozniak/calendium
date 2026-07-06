import type {
  AvailabilitySlot,
  Calendar,
  Event,
  EventInput,
  EventPatch,
  RsvpStatus,
} from '@calendium/shared';

import { getApiClient } from '@/lib/api';
import { calendarMock } from '@/lib/calendar-mock';

/**
 * Data-access wrappers for the calendar UI: hit the real API first and fall
 * back transparently to the in-memory mock (lib/calendar-mock.ts) while the
 * Go backend is not reachable (local dev / demo).
 */

export async function fetchCalendars(): Promise<Calendar[]> {
  try {
    return await getApiClient().listCalendars();
  } catch {
    return calendarMock.listCalendars();
  }
}

export async function patchCalendar(
  id: string,
  patch: Partial<Pick<Calendar, 'isVisible' | 'color'>>
): Promise<Calendar> {
  try {
    return await getApiClient().updateCalendar(id, patch);
  } catch {
    return calendarMock.updateCalendar(id, patch);
  }
}

export async function fetchEvents(from: Date, to: Date): Promise<Event[]> {
  try {
    return await getApiClient().listEvents(from.toISOString(), to.toISOString());
  } catch {
    return calendarMock.listEvents(from.toISOString(), to.toISOString());
  }
}

export async function createEventApi(input: EventInput): Promise<Event> {
  try {
    return await getApiClient().createEvent(input);
  } catch {
    return calendarMock.createEvent(input);
  }
}

export async function updateEventApi(id: string, patch: EventPatch): Promise<Event> {
  try {
    return await getApiClient().updateEvent(id, patch);
  } catch {
    return calendarMock.updateEvent(id, patch);
  }
}

export async function deleteEventApi(id: string): Promise<void> {
  try {
    await getApiClient().deleteEvent(id);
  } catch {
    calendarMock.deleteEvent(id);
  }
}

export async function sendRsvpApi(id: string, response: RsvpStatus): Promise<Event> {
  try {
    return await getApiClient().rsvp(id, response);
  } catch {
    return calendarMock.rsvp(id, response);
  }
}

export async function fetchAvailability(
  from: Date,
  to: Date,
  durationMinutes: number
): Promise<AvailabilitySlot[]> {
  try {
    return await getApiClient().getAvailability(from.toISOString(), to.toISOString(), durationMinutes);
  } catch {
    return calendarMock.availability(from.toISOString(), to.toISOString(), durationMinutes);
  }
}
