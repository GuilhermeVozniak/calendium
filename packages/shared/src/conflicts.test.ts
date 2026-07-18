import { describe, expect, it } from 'vitest';

import {
  type BusyInterval,
  findConflicts,
  mergeBusy,
  overlaps,
  suggestFreeSlots,
  toBusyIntervals,
} from './conflicts';
import type { Attendee, Calendar, Event } from './types';

const OWN_EMAILS = ['me@example.com', 'me@work.example.com'];

function attendee(email: string, response: Attendee['response'] = 'accepted'): Attendee {
  return { email, name: null, response, organizer: false, optional: false };
}

function makeEvent(overrides: Partial<Event> & Pick<Event, 'id' | 'start' | 'end'>): Event {
  return {
    calendarId: 'cal1',
    title: 'Event',
    description: null,
    location: null,
    allDay: false,
    recurrenceRule: null,
    attendees: [],
    conferencing: null,
    status: 'confirmed',
    visibility: 'default',
    reminderMinutes: [],
    ...overrides,
  };
}

function makeCalendar(overrides: Partial<Calendar> & Pick<Calendar, 'id' | 'accountId'>): Calendar {
  return {
    name: 'Calendar',
    color: '#000000',
    timeZone: 'UTC',
    isPrimary: false,
    isVisible: true,
    canWrite: true,
    ...overrides,
  };
}

const CALENDARS: Calendar[] = [
  makeCalendar({ id: 'cal1', accountId: 'acc1' }),
  makeCalendar({ id: 'cal2', accountId: 'acc2' }),
];

// ---------------------------------------------------------------------------
// overlaps — half-open interval test
// ---------------------------------------------------------------------------

describe('overlaps', () => {
  it('is true when intervals genuinely overlap', () => {
    expect(
      overlaps(
        new Date('2026-01-01T09:00:00Z'),
        new Date('2026-01-01T10:00:00Z'),
        new Date('2026-01-01T09:30:00Z'),
        new Date('2026-01-01T10:30:00Z')
      )
    ).toBe(true);
  });

  it('is false when one interval ends exactly when the other starts (touching)', () => {
    expect(
      overlaps(
        new Date('2026-01-01T09:00:00Z'),
        new Date('2026-01-01T10:00:00Z'),
        new Date('2026-01-01T10:00:00Z'),
        new Date('2026-01-01T11:00:00Z')
      )
    ).toBe(false);
  });

  it('is false for fully disjoint intervals', () => {
    expect(
      overlaps(
        new Date('2026-01-01T09:00:00Z'),
        new Date('2026-01-01T10:00:00Z'),
        new Date('2026-01-01T11:00:00Z'),
        new Date('2026-01-01T12:00:00Z')
      )
    ).toBe(false);
  });
});

// ---------------------------------------------------------------------------
// toBusyIntervals
// ---------------------------------------------------------------------------

describe('toBusyIntervals', () => {
  it('produces a BusyInterval for a normal confirmed event', () => {
    const events = [
      makeEvent({
        id: 'e1',
        calendarId: 'cal1',
        title: 'Standup',
        start: '2026-01-01T09:00:00Z',
        end: '2026-01-01T09:30:00Z',
      }),
    ];
    const result = toBusyIntervals(events, CALENDARS, OWN_EMAILS);
    expect(result).toEqual([
      {
        start: new Date('2026-01-01T09:00:00Z'),
        end: new Date('2026-01-01T09:30:00Z'),
        eventId: 'e1',
        calendarId: 'cal1',
        accountId: 'acc1',
        title: 'Standup',
      },
    ]);
  });

  it('excludes cancelled events', () => {
    const events = [
      makeEvent({
        id: 'e1',
        start: '2026-01-01T09:00:00Z',
        end: '2026-01-01T09:30:00Z',
        status: 'cancelled',
      }),
    ];
    expect(toBusyIntervals(events, CALENDARS, OWN_EMAILS)).toEqual([]);
  });

  it('excludes all-day events', () => {
    const events = [
      makeEvent({
        id: 'e1',
        start: '2026-01-01T00:00:00Z',
        end: '2026-01-02T00:00:00Z',
        allDay: true,
      }),
    ];
    expect(toBusyIntervals(events, CALENDARS, OWN_EMAILS)).toEqual([]);
  });

  it('excludes events the user has declined', () => {
    const events = [
      makeEvent({
        id: 'e1',
        start: '2026-01-01T09:00:00Z',
        end: '2026-01-01T09:30:00Z',
        attendees: [attendee('me@example.com', 'declined'), attendee('other@example.com')],
      }),
    ];
    expect(toBusyIntervals(events, CALENDARS, OWN_EMAILS)).toEqual([]);
  });

  it('keeps events the user accepted or has not responded to', () => {
    const events = [
      makeEvent({
        id: 'e1',
        start: '2026-01-01T09:00:00Z',
        end: '2026-01-01T09:30:00Z',
        attendees: [attendee('me@example.com', 'tentative')],
      }),
    ];
    expect(toBusyIntervals(events, CALENDARS, OWN_EMAILS)).toHaveLength(1);
  });

  it('excludes the event matching ignoreEventId (the one being edited)', () => {
    const events = [
      makeEvent({ id: 'e1', start: '2026-01-01T09:00:00Z', end: '2026-01-01T09:30:00Z' }),
      makeEvent({ id: 'e2', start: '2026-01-01T10:00:00Z', end: '2026-01-01T10:30:00Z' }),
    ];
    const result = toBusyIntervals(events, CALENDARS, OWN_EMAILS, { ignoreEventId: 'e1' });
    expect(result.map((b) => b.eventId)).toEqual(['e2']);
  });

  it('resolves accountId per event from its calendar, across accounts', () => {
    const events = [
      makeEvent({ id: 'e1', calendarId: 'cal1', start: '2026-01-01T09:00:00Z', end: '2026-01-01T09:30:00Z' }),
      makeEvent({ id: 'e2', calendarId: 'cal2', start: '2026-01-01T10:00:00Z', end: '2026-01-01T10:30:00Z' }),
    ];
    const result = toBusyIntervals(events, CALENDARS, OWN_EMAILS);
    expect(result.map((b) => [b.eventId, b.accountId])).toEqual([
      ['e1', 'acc1'],
      ['e2', 'acc2'],
    ]);
  });
});

// ---------------------------------------------------------------------------
// mergeBusy
// ---------------------------------------------------------------------------

function busy(
  startIso: string,
  endIso: string,
  overrides: Partial<BusyInterval> = {}
): BusyInterval {
  return {
    start: new Date(startIso),
    end: new Date(endIso),
    eventId: overrides.eventId ?? 'e',
    calendarId: overrides.calendarId ?? 'cal1',
    accountId: overrides.accountId ?? 'acc1',
    title: overrides.title ?? 'Event',
  };
}

describe('mergeBusy', () => {
  it('returns an empty array for no intervals', () => {
    expect(mergeBusy([])).toEqual([]);
  });

  it('leaves disjoint intervals separate', () => {
    const result = mergeBusy([
      busy('2026-01-01T09:00:00Z', '2026-01-01T09:30:00Z'),
      busy('2026-01-01T10:00:00Z', '2026-01-01T10:30:00Z'),
    ]);
    expect(result).toEqual([
      { start: new Date('2026-01-01T09:00:00Z'), end: new Date('2026-01-01T09:30:00Z') },
      { start: new Date('2026-01-01T10:00:00Z'), end: new Date('2026-01-01T10:30:00Z') },
    ]);
  });

  it('merges overlapping intervals', () => {
    const result = mergeBusy([
      busy('2026-01-01T09:00:00Z', '2026-01-01T10:00:00Z'),
      busy('2026-01-01T09:30:00Z', '2026-01-01T10:30:00Z'),
    ]);
    expect(result).toEqual([
      { start: new Date('2026-01-01T09:00:00Z'), end: new Date('2026-01-01T10:30:00Z') },
    ]);
  });

  it('merges adjacent (touching) intervals into a single chain', () => {
    const result = mergeBusy([
      busy('2026-01-01T09:00:00Z', '2026-01-01T10:00:00Z'),
      busy('2026-01-01T10:00:00Z', '2026-01-01T11:00:00Z'),
      busy('2026-01-01T11:00:00Z', '2026-01-01T12:00:00Z'),
    ]);
    expect(result).toEqual([
      { start: new Date('2026-01-01T09:00:00Z'), end: new Date('2026-01-01T12:00:00Z') },
    ]);
  });

  it('merges a fully nested interval without extending the outer range', () => {
    const result = mergeBusy([
      busy('2026-01-01T09:00:00Z', '2026-01-01T12:00:00Z'),
      busy('2026-01-01T10:00:00Z', '2026-01-01T10:30:00Z'),
    ]);
    expect(result).toEqual([
      { start: new Date('2026-01-01T09:00:00Z'), end: new Date('2026-01-01T12:00:00Z') },
    ]);
  });

  it('handles intervals out of input order', () => {
    const result = mergeBusy([
      busy('2026-01-01T11:00:00Z', '2026-01-01T12:00:00Z'),
      busy('2026-01-01T09:00:00Z', '2026-01-01T09:30:00Z'),
    ]);
    expect(result).toEqual([
      { start: new Date('2026-01-01T09:00:00Z'), end: new Date('2026-01-01T09:30:00Z') },
      { start: new Date('2026-01-01T11:00:00Z'), end: new Date('2026-01-01T12:00:00Z') },
    ]);
  });
});

// ---------------------------------------------------------------------------
// findConflicts
// ---------------------------------------------------------------------------

describe('findConflicts', () => {
  const BUSY = [
    busy('2026-01-01T09:00:00Z', '2026-01-01T10:00:00Z', { eventId: 'e1' }),
    busy('2026-01-01T14:00:00Z', '2026-01-01T15:00:00Z', { eventId: 'e2' }),
  ];

  it('returns events overlapping the candidate slot', () => {
    const result = findConflicts(
      new Date('2026-01-01T09:30:00Z'),
      new Date('2026-01-01T10:30:00Z'),
      BUSY
    );
    expect(result.map((b) => b.eventId)).toEqual(['e1']);
  });

  it('returns an empty array when the slot merely touches a busy interval', () => {
    const result = findConflicts(
      new Date('2026-01-01T10:00:00Z'),
      new Date('2026-01-01T11:00:00Z'),
      BUSY
    );
    expect(result).toEqual([]);
  });

  it('returns an empty array when nothing overlaps', () => {
    const result = findConflicts(
      new Date('2026-01-01T11:00:00Z'),
      new Date('2026-01-01T12:00:00Z'),
      BUSY
    );
    expect(result).toEqual([]);
  });

  it('returns every overlapping interval when the slot spans multiple', () => {
    const result = findConflicts(
      new Date('2026-01-01T08:00:00Z'),
      new Date('2026-01-01T16:00:00Z'),
      BUSY
    );
    expect(result.map((b) => b.eventId)).toEqual(['e1', 'e2']);
  });
});

// ---------------------------------------------------------------------------
// suggestFreeSlots
// ---------------------------------------------------------------------------

describe('suggestFreeSlots', () => {
  const FROM = new Date('2026-01-01T09:00:00Z');
  const TO = new Date('2026-01-01T17:00:00Z');

  it('returns the whole range when there is no busy time', () => {
    const result = suggestFreeSlots(FROM, TO, 30, []);
    expect(result).toEqual([{ start: FROM, end: TO }]);
  });

  it('finds a free slot at the head of the range (before the first busy interval)', () => {
    const result = suggestFreeSlots(FROM, TO, 30, [
      busy('2026-01-01T10:00:00Z', '2026-01-01T17:00:00Z'),
    ]);
    expect(result).toEqual([{ start: FROM, end: new Date('2026-01-01T10:00:00Z') }]);
  });

  it('finds a free slot in the middle, between two busy intervals', () => {
    const result = suggestFreeSlots(FROM, TO, 30, [
      busy('2026-01-01T09:00:00Z', '2026-01-01T11:00:00Z'),
      busy('2026-01-01T11:30:00Z', '2026-01-01T17:00:00Z'),
    ]);
    expect(result).toEqual([
      { start: new Date('2026-01-01T11:00:00Z'), end: new Date('2026-01-01T11:30:00Z') },
    ]);
  });

  it('finds a free slot at the tail of the range (after the last busy interval)', () => {
    const result = suggestFreeSlots(FROM, TO, 30, [
      busy('2026-01-01T09:00:00Z', '2026-01-01T16:00:00Z'),
    ]);
    expect(result).toEqual([{ start: new Date('2026-01-01T16:00:00Z'), end: TO }]);
  });

  it('omits gaps shorter than the requested duration', () => {
    const result = suggestFreeSlots(FROM, TO, 30, [
      busy('2026-01-01T09:00:00Z', '2026-01-01T11:00:00Z'),
      busy('2026-01-01T11:15:00Z', '2026-01-01T17:00:00Z'),
    ]);
    expect(result).toEqual([]);
  });

  it('ignores busy intervals entirely outside [from, to)', () => {
    const result = suggestFreeSlots(FROM, TO, 30, [
      busy('2026-01-01T05:00:00Z', '2026-01-01T06:00:00Z'),
      busy('2026-01-01T20:00:00Z', '2026-01-01T21:00:00Z'),
    ]);
    expect(result).toEqual([{ start: FROM, end: TO }]);
  });
});
