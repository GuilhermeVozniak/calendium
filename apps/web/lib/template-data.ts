import { addDays, addMinutes, startOfDay } from 'date-fns';

import type { EventInput, EventTemplate, EventTemplateInput } from '@calendium/shared';

import { getApiClient } from '@/lib/api';
import { DEMO_MODE } from '@/lib/demo';
import { calendarMock } from '@/lib/calendar-mock';

/**
 * Data-access wrappers for saved event templates. They hit the real API and,
 * in explicit demo mode only (lib/demo.ts), fall back to the in-memory mock
 * (lib/calendar-mock.ts). Outside demo mode failures propagate so the UI shows
 * real loading / empty / error states and mutations surface their failures.
 */

export async function fetchEventTemplates(): Promise<EventTemplate[]> {
  try {
    return await getApiClient().listEventTemplates();
  } catch (err) {
    if (DEMO_MODE) return calendarMock.listEventTemplates();
    throw err;
  }
}

export async function createEventTemplateApi(input: EventTemplateInput): Promise<EventTemplate> {
  try {
    return await getApiClient().createEventTemplate(input);
  } catch (err) {
    if (DEMO_MODE) return calendarMock.createEventTemplate(input);
    throw err;
  }
}

export async function updateEventTemplateApi(
  id: string,
  input: EventTemplateInput
): Promise<EventTemplate> {
  try {
    return await getApiClient().updateEventTemplate(id, input);
  } catch (err) {
    if (DEMO_MODE) return calendarMock.updateEventTemplate(id, input);
    throw err;
  }
}

export async function deleteEventTemplateApi(id: string): Promise<void> {
  try {
    await getApiClient().deleteEventTemplate(id);
  } catch (err) {
    if (DEMO_MODE) {
      calendarMock.deleteEventTemplate(id);
      return;
    }
    throw err;
  }
}

/**
 * Fire-and-forget usage ping ("used N times" in the manager). Not on the
 * critical path of applying a template, so failures - including demo mode's
 * synchronous mock - are swallowed rather than surfaced to the user.
 */
export function recordTemplateUsage(id: string): void {
  if (DEMO_MODE) {
    calendarMock.markTemplateUsed(id);
    return;
  }
  // biome-ignore lint/correctness/useHookAtTopLevel: ApiClient.useEventTemplate is a plain async method (POST /v1/event-templates/{id}/use, Task 5's contract), not a React hook - the name only coincidentally matches the "use*" naming heuristic.
  void getApiClient()
    .useEventTemplate(id)
    .catch(() => {});
}

/**
 * Expands a saved template into event-creation defaults anchored at `at`.
 * Leaves `calendarId` unset when the template didn't pin one so the caller's
 * own "primary writable calendar" fallback (event-dialog.tsx) applies -
 * this function only knows the template, not the user's calendar list.
 */
export function applyTemplate(template: EventTemplate, at: Date): Partial<EventInput> {
  const start = template.allDay ? startOfDay(at) : at;
  const end = template.allDay
    ? addDays(start, 1)
    : addMinutes(start, template.durationMinutes);

  return {
    calendarId: template.calendarId ?? undefined,
    title: template.title,
    description: template.description || undefined,
    location: template.location || undefined,
    start: start.toISOString(),
    end: end.toISOString(),
    allDay: template.allDay,
    attendeeEmails: template.attendeeEmails.length > 0 ? [...template.attendeeEmails] : undefined,
    addConferencing: template.addConferencing || undefined,
    reminderMinutes:
      template.reminderMinutes.length > 0 ? [...template.reminderMinutes] : undefined,
    recurrenceRule: template.recurrenceRule ?? undefined,
  };
}
