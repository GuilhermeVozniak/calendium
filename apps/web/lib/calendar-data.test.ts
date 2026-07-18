import type { Event } from '@calendium/shared';
import { beforeEach, describe, expect, it, vi } from 'vitest';

// ---------------------------------------------------------------------------
// Mocks
// ---------------------------------------------------------------------------

// Toggleable DEMO_MODE (see apps/web/lib/use-mail-triage.test.tsx for the
// pattern this follows): a getter keeps calendar-data.ts's live DEMO_MODE
// import in sync per-test without vi.resetModules().
const demoState = vi.hoisted(() => ({ value: false }));
vi.mock('@/lib/demo', () => ({
  get DEMO_MODE() {
    return demoState.value;
  },
}));

const listEventsMock = vi.fn();
vi.mock('@/lib/api', () => ({
  getApiClient: () => ({
    listEvents: (...args: unknown[]) => listEventsMock(...args),
  }),
}));

const mockListEventsMock = vi.fn();
vi.mock('@/lib/calendar-mock', () => ({
  calendarMock: {
    listEvents: (...args: unknown[]) => mockListEventsMock(...args),
  },
}));

import { fetchBusyEvents } from '@/lib/calendar-data';
import type { CalendarSet } from '@calendium/shared';

const FROM = new Date('2026-07-20T00:00:00.000Z');
const TO = new Date('2026-07-21T00:00:00.000Z');

function hiddenCalendarEvent(): Event {
  return {
    id: 'evt-hidden-1',
    calendarId: 'cal-hidden-other-account',
    title: 'Dentist',
    description: null,
    location: null,
    start: '2026-07-20T15:00:00.000Z',
    end: '2026-07-20T15:45:00.000Z',
    allDay: false,
    recurrenceRule: null,
    attendees: [],
    conferencing: null,
    status: 'confirmed',
    visibility: 'default',
    reminderMinutes: [],
  };
}

beforeEach(() => {
  vi.clearAllMocks();
  demoState.value = false;
});

describe('fetchBusyEvents', () => {
  it('queries the full range with no calendarIds filter, so hidden/other-account calendars are included', async () => {
    const events = [hiddenCalendarEvent()];
    listEventsMock.mockResolvedValue(events);

    const result = await fetchBusyEvents(FROM, TO);

    expect(listEventsMock).toHaveBeenCalledWith(FROM.toISOString(), TO.toISOString());
    expect(listEventsMock.mock.calls[0]).toHaveLength(2); // no calendarIds arg
    expect(result).toBe(events);
  });

  it('falls back to the demo mock (also unfiltered) when the API fails and DEMO_MODE is on', async () => {
    demoState.value = true;
    listEventsMock.mockRejectedValue(new Error('offline'));
    const events = [hiddenCalendarEvent()];
    mockListEventsMock.mockReturnValue(events);

    const result = await fetchBusyEvents(FROM, TO);

    expect(mockListEventsMock).toHaveBeenCalledWith(FROM.toISOString(), TO.toISOString());
    expect(result).toBe(events);
  });

  it('propagates the API failure outside demo mode instead of silently returning mock data', async () => {
    listEventsMock.mockRejectedValue(new Error('offline'));

    await expect(fetchBusyEvents(FROM, TO)).rejects.toThrow('offline');
    expect(mockListEventsMock).not.toHaveBeenCalled();
  });
});


