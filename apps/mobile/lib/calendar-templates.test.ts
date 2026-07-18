import type { EventTemplate } from '@calendium/shared';
import { applyTemplate } from './calendar-templates';

function makeTemplate(overrides: Partial<EventTemplate> = {}): EventTemplate {
  return {
    id: 'tpl_1',
    name: '1:1',
    title: '1:1 with Ana',
    description: '',
    location: '',
    durationMinutes: 30,
    allDay: false,
    calendarId: null,
    attendeeEmails: [],
    addConferencing: false,
    reminderMinutes: [],
    recurrenceRule: null,
    usageCount: 0,
    ...overrides,
  };
}

describe('applyTemplate', () => {
  const at = new Date('2026-08-03T14:00:00Z');

  it('prefills title/start/end using the template duration', () => {
    const result = applyTemplate(makeTemplate({ durationMinutes: 45 }), at);
    expect(result.title).toBe('1:1 with Ana');
    expect(result.start).toBe(at.toISOString());
    expect(result.end).toBe(new Date(at.getTime() + 45 * 60_000).toISOString());
    expect(result.allDay).toBe(false);
  });

  it('expands an all-day template to the full day starting at `at`', () => {
    const result = applyTemplate(makeTemplate({ allDay: true }), at);
    const expectedStart = new Date(at);
    expectedStart.setHours(0, 0, 0, 0);
    expect(result.start).toBe(expectedStart.toISOString());
    expect(result.end).toBe(new Date(expectedStart.getTime() + 86_400_000).toISOString());
    expect(result.allDay).toBe(true);
  });

  it('carries over optional fields when present', () => {
    const result = applyTemplate(
      makeTemplate({
        description: 'Weekly sync',
        location: 'Room 4B',
        calendarId: 'cal_work',
        attendeeEmails: ['ana@example.com'],
        addConferencing: true,
        reminderMinutes: [10, 30],
        recurrenceRule: 'FREQ=WEEKLY',
      }),
      at
    );
    expect(result).toMatchObject({
      description: 'Weekly sync',
      location: 'Room 4B',
      calendarId: 'cal_work',
      attendeeEmails: ['ana@example.com'],
      addConferencing: true,
      reminderMinutes: [10, 30],
      recurrenceRule: 'FREQ=WEEKLY',
    });
  });

  it('leaves optional empty fields undefined rather than fabricating values', () => {
    const result = applyTemplate(makeTemplate(), at);
    expect(result.calendarId).toBeUndefined();
    expect(result.description).toBeUndefined();
    expect(result.location).toBeUndefined();
    expect(result.attendeeEmails).toBeUndefined();
    expect(result.addConferencing).toBeUndefined();
    expect(result.reminderMinutes).toBeUndefined();
    expect(result.recurrenceRule).toBeUndefined();
  });
});
