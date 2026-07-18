// Applies a saved event template to prefill the mobile create-event sheet
// (Task 20). Re-implements web's lib/template-data.ts `applyTemplate`
// (Task 17) semantics locally over the shared `EventTemplate`/`EventInput`
// types, using lib/format.ts's native-Date helpers instead of date-fns
// (the mobile app doesn't depend on it).
import { addDays, startOfDay } from '@/lib/format';
import type { EventInput, EventTemplate } from '@calendium/shared';

/**
 * Expands a saved template into event-creation defaults anchored at `at`.
 * Leaves `calendarId` unset when the template didn't pin one so the caller's
 * own "primary writable calendar" fallback applies. All-day templates start
 * at midnight of `at`'s day and span exactly one day; timed templates start
 * exactly at `at` and run for the template's duration.
 */
export function applyTemplate(template: EventTemplate, at: Date): Partial<EventInput> {
  const start = template.allDay ? startOfDay(at) : new Date(at);
  const end = template.allDay
    ? addDays(start, 1)
    : new Date(start.getTime() + template.durationMinutes * 60_000);

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
