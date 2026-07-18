import { describe, expect, it } from 'vitest';

import { nextHalfHour, parseQuickAdd } from '@/lib/quick-add';

// Fixed reference instant used by every test for determinism: Wednesday,
// January 10, 2024, 14:00 local time.
const REF = new Date(2024, 0, 10, 14, 0, 0, 0);

function hm(d: Date): string {
  return `${d.getFullYear()}-${d.getMonth() + 1}-${d.getDate()} ${d.getHours()}:${String(d.getMinutes()).padStart(2, '0')}`;
}

describe('parseQuickAdd', () => {
  it('returns null for empty input', () => {
    expect(parseQuickAdd('', REF)).toBeNull();
  });

  it('returns null for whitespace-only input', () => {
    expect(parseQuickAdd('   ', REF)).toBeNull();
  });

  it('returns matched:false with the raw text as title when nothing is recognized', () => {
    const parsed = parseQuickAdd('Buy milk', REF);
    expect(parsed).not.toBeNull();
    expect(parsed!.matched).toBe(false);
    expect(parsed!.title).toBe('Buy milk');
    expect(parsed!.allDay).toBe(false);
    expect(hm(parsed!.end)).toBe(hm(new Date(parsed!.start.getTime() + 60 * 60 * 1000)));
  });

  describe('time ranges', () => {
    it('parses an unmarked range defaulting to PM ("12:30-1:30")', () => {
      const parsed = parseQuickAdd('lunch 12:30-1:30', REF)!;
      expect(parsed.title).toBe('lunch');
      expect(hm(parsed.start)).toBe('2024-1-10 12:30');
      expect(hm(parsed.end)).toBe('2024-1-10 13:30');
      expect(parsed.matched).toBe(true);
    });

    it('borrows the trailing meridiem for the start time ("2 to 3pm")', () => {
      const parsed = parseQuickAdd('call 2 to 3pm', REF)!;
      expect(parsed.title).toBe('call');
      expect(hm(parsed.start)).toBe('2024-1-10 14:00');
      expect(hm(parsed.end)).toBe('2024-1-10 15:00');
    });

    it('rolls an end time past midnight forward ("11-1")', () => {
      const parsed = parseQuickAdd('lunch 11-1', REF)!;
      expect(hm(parsed.start)).toBe('2024-1-10 11:00');
      expect(hm(parsed.end)).toBe('2024-1-10 13:00');
    });

    it('applies the "early hours mean PM" heuristic to both ends ("3-4")', () => {
      const parsed = parseQuickAdd('call 3-4', REF)!;
      expect(hm(parsed.start)).toBe('2024-1-10 15:00');
      expect(hm(parsed.end)).toBe('2024-1-10 16:00');
    });
  });

  describe('single times', () => {
    it('parses "at <hour>"', () => {
      const parsed = parseQuickAdd('call with Sam at 3', REF)!;
      expect(parsed.title).toBe('call with Sam');
      expect(hm(parsed.start)).toBe('2024-1-10 15:00');
      expect(hm(parsed.end)).toBe('2024-1-10 16:00');
    });

    it('parses clock times without meridiem using the PM heuristic ("3:15")', () => {
      const parsed = parseQuickAdd('dentist 3:15', REF)!;
      expect(hm(parsed.start)).toBe('2024-1-10 15:15');
    });

    it('keeps clock times at/after 8am as-is when unmarked ("9:15")', () => {
      const parsed = parseQuickAdd('standup 9:15', REF)!;
      expect(hm(parsed.start)).toBe('2024-1-10 9:15');
    });

    it('parses explicit meridiem times ("7pm")', () => {
      const parsed = parseQuickAdd('dentist 7pm', REF)!;
      expect(parsed.title).toBe('dentist');
      expect(hm(parsed.start)).toBe('2024-1-10 19:00');
    });

    it('parses "noon"', () => {
      const parsed = parseQuickAdd('lunch noon', REF)!;
      expect(parsed.title).toBe('lunch');
      expect(hm(parsed.start)).toBe('2024-1-10 12:00');
    });

    it('parses "midnight"', () => {
      const parsed = parseQuickAdd('midnight snack', REF)!;
      expect(parsed.title).toBe('snack');
      expect(hm(parsed.start)).toBe('2024-1-10 0:00');
    });
  });

  describe('durations', () => {
    it('applies an explicit duration in minutes', () => {
      const parsed = parseQuickAdd('standup for 45 min', REF)!;
      expect(parsed.title).toBe('standup');
      expect(hm(parsed.end)).toBe(hm(new Date(parsed.start.getTime() + 45 * 60 * 1000)));
    });

    it('applies an explicit duration in hours', () => {
      const parsed = parseQuickAdd('workshop at 9 for 2 hours', REF)!;
      expect(hm(parsed.start)).toBe('2024-1-10 9:00');
      expect(hm(parsed.end)).toBe('2024-1-10 11:00');
    });
  });

  describe('relative days', () => {
    it('parses "today", bumping to the next half hour when the default 9am has passed', () => {
      const parsed = parseQuickAdd('coffee today', REF)!;
      expect(parsed.title).toBe('coffee');
      // REF is 14:00, so the default 9am slot has already passed.
      expect(hm(parsed.start)).toBe('2024-1-10 14:30');
    });

    it('parses "tonight" as 7pm', () => {
      const parsed = parseQuickAdd('coffee tonight', REF)!;
      expect(hm(parsed.start)).toBe('2024-1-10 19:00');
    });

    it('parses "tomorrow" combined with an explicit time', () => {
      const parsed = parseQuickAdd('standup tomorrow at 9:30', REF)!;
      expect(parsed.title).toBe('standup');
      expect(hm(parsed.start)).toBe('2024-1-11 9:30');
    });
  });

  describe('weekdays', () => {
    it('parses a bare weekday as the next upcoming occurrence', () => {
      const parsed = parseQuickAdd('team sync friday at 2pm', REF)!;
      expect(parsed.title).toBe('team sync');
      expect(hm(parsed.start)).toBe('2024-1-12 14:00');
    });

    it('treats the current weekday as next week, never today', () => {
      const parsed = parseQuickAdd('sync wednesday', REF)!;
      expect(hm(parsed.start)).toBe('2024-1-17 9:00');
    });

    it('honors an explicit "next" prefix', () => {
      const parsed = parseQuickAdd('review next monday', REF)!;
      expect(parsed.title).toBe('review');
      expect(hm(parsed.start)).toBe('2024-1-15 9:00');
    });

    it('strips a dangling preposition left after removing the weekday', () => {
      const parsed = parseQuickAdd('checkin on friday', REF)!;
      expect(parsed.title).toBe('checkin');
      expect(hm(parsed.start)).toBe('2024-1-12 9:00');
    });
  });

  describe('explicit dates', () => {
    it('parses "<month> <day>" in the future this year', () => {
      const parsed = parseQuickAdd('trip mar 5', REF)!;
      expect(parsed.title).toBe('trip');
      expect(hm(parsed.start)).toBe('2024-3-5 9:00');
    });

    it('rolls "<month> <day>" already past this year to next year', () => {
      const parsed = parseQuickAdd('party jan 1', REF)!;
      expect(parsed.title).toBe('party');
      expect(hm(parsed.start)).toBe('2025-1-1 9:00');
    });

    it('parses numeric "M/D" in the future this year', () => {
      const parsed = parseQuickAdd('meeting 7/24', REF)!;
      expect(parsed.title).toBe('meeting');
      expect(hm(parsed.start)).toBe('2024-7-24 9:00');
    });

    it('rolls numeric "M/D" already past this year to next year', () => {
      const parsed = parseQuickAdd('renew 1/2', REF)!;
      expect(hm(parsed.start)).toBe('2025-1-2 9:00');
    });

    it('honors an explicit year in "M/D/YY" without rolling it forward', () => {
      const parsed = parseQuickAdd('meeting 1/5/23', REF)!;
      expect(hm(parsed.start)).toBe('2023-1-5 9:00');
    });
  });

  it('falls back to "New event" when the whole input is consumed by matches', () => {
    const parsed = parseQuickAdd('tomorrow at 3pm', REF)!;
    expect(parsed.title).toBe('New event');
  });

  describe('all-day', () => {
    it('parses "all day" combined with an explicit date', () => {
      const parsed = parseQuickAdd('offsite jul 24 all day', REF)!;
      expect(parsed.title).toBe('offsite');
      expect(parsed.allDay).toBe(true);
      expect(hm(parsed.start)).toBe('2024-7-24 0:00');
      expect(hm(parsed.end)).toBe('2024-7-25 0:00');
    });

    it('parses the hyphenated "all-day" variant', () => {
      const parsed = parseQuickAdd('vacation all-day', REF)!;
      expect(parsed.title).toBe('vacation');
      expect(parsed.allDay).toBe(true);
    });

    it('leaves allDay false when not requested', () => {
      const parsed = parseQuickAdd('lunch 12:30-1:30', REF)!;
      expect(parsed.allDay).toBe(false);
    });
  });

  describe('recurrence', () => {
    it('parses "daily"', () => {
      const parsed = parseQuickAdd('standup daily', REF)!;
      expect(parsed.title).toBe('standup');
      expect(parsed.recurrenceRule).toBe('FREQ=DAILY');
    });

    it('parses "every day"', () => {
      const parsed = parseQuickAdd('standup every day', REF)!;
      expect(parsed.recurrenceRule).toBe('FREQ=DAILY');
    });

    it('parses "weekly"', () => {
      const parsed = parseQuickAdd('sync weekly', REF)!;
      expect(parsed.recurrenceRule).toBe('FREQ=WEEKLY');
    });

    it('parses "every week"', () => {
      const parsed = parseQuickAdd('sync every week', REF)!;
      expect(parsed.recurrenceRule).toBe('FREQ=WEEKLY');
    });

    it('parses "biweekly"', () => {
      const parsed = parseQuickAdd('check-in biweekly', REF)!;
      expect(parsed.recurrenceRule).toBe('FREQ=WEEKLY;INTERVAL=2');
    });

    it('parses "every other week"', () => {
      const parsed = parseQuickAdd('check-in every other week', REF)!;
      expect(parsed.recurrenceRule).toBe('FREQ=WEEKLY;INTERVAL=2');
    });

    it('parses "monthly"', () => {
      const parsed = parseQuickAdd('rent monthly', REF)!;
      expect(parsed.recurrenceRule).toBe('FREQ=MONTHLY');
    });

    it('parses "every month"', () => {
      const parsed = parseQuickAdd('review every month', REF)!;
      expect(parsed.recurrenceRule).toBe('FREQ=MONTHLY');
    });

    it('parses "yearly"', () => {
      const parsed = parseQuickAdd('anniversary yearly', REF)!;
      expect(parsed.recurrenceRule).toBe('FREQ=YEARLY');
    });

    it('parses "annually"', () => {
      const parsed = parseQuickAdd('anniversary annually', REF)!;
      expect(parsed.recurrenceRule).toBe('FREQ=YEARLY');
    });

    it('parses "every year"', () => {
      const parsed = parseQuickAdd('anniversary every year', REF)!;
      expect(parsed.recurrenceRule).toBe('FREQ=YEARLY');
    });

    it('parses "every N days"', () => {
      const parsed = parseQuickAdd('backup every 3 days', REF)!;
      expect(parsed.title).toBe('backup');
      expect(parsed.recurrenceRule).toBe('FREQ=DAILY;INTERVAL=3');
    });

    it('parses "every N weeks"', () => {
      const parsed = parseQuickAdd('retro every 2 weeks', REF)!;
      expect(parsed.recurrenceRule).toBe('FREQ=WEEKLY;INTERVAL=2');
    });

    it('parses "every N months"', () => {
      const parsed = parseQuickAdd('review every 6 months', REF)!;
      expect(parsed.recurrenceRule).toBe('FREQ=MONTHLY;INTERVAL=6');
    });

    it('parses "every N years"', () => {
      const parsed = parseQuickAdd('checkup every 5 years', REF)!;
      expect(parsed.recurrenceRule).toBe('FREQ=YEARLY;INTERVAL=5');
    });

    it('parses "every weekday" and anchors to the next Monday', () => {
      const parsed = parseQuickAdd('standup every weekday', REF)!;
      expect(parsed.title).toBe('standup');
      expect(parsed.recurrenceRule).toBe('FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR');
      expect(hm(parsed.start)).toBe('2024-1-15 9:00');
    });

    it('parses "every <weekday>" and anchors to its next occurrence, without also matching a one-off date', () => {
      const parsed = parseQuickAdd('every monday', REF)!;
      expect(parsed.recurrenceRule).toBe('FREQ=WEEKLY;BYDAY=MO');
      expect(parsed.title).toBe('New event');
      expect(hm(parsed.start)).toBe('2024-1-15 9:00');
    });

    it('parses a weekday list joined by "and"', () => {
      const parsed = parseQuickAdd('team sync every mon and wed', REF)!;
      expect(parsed.title).toBe('team sync');
      expect(parsed.recurrenceRule).toBe('FREQ=WEEKLY;BYDAY=MO,WE');
    });

    it('parses a weekday list joined by a comma', () => {
      const parsed = parseQuickAdd('class every tue, thu', REF)!;
      expect(parsed.recurrenceRule).toBe('FREQ=WEEKLY;BYDAY=TU,TH');
    });

    it('parses "every other <weekday>" and anchors to its next occurrence', () => {
      const parsed = parseQuickAdd('gym every other tuesday', REF)!;
      expect(parsed.recurrenceRule).toBe('FREQ=WEEKLY;INTERVAL=2;BYDAY=TU');
      expect(hm(parsed.start)).toBe('2024-1-16 9:00');
    });

    it('appends UNTIL from a trailing "until <month day>"', () => {
      const parsed = parseQuickAdd('policy monthly until dec 31', REF)!;
      expect(parsed.recurrenceRule).toBe('FREQ=MONTHLY;UNTIL=20241231T000000Z');
    });

    it('appends UNTIL from a trailing "until <M/D>"', () => {
      const parsed = parseQuickAdd('sync weekly until 3/1', REF)!;
      expect(parsed.recurrenceRule).toBe('FREQ=WEEKLY;UNTIL=20240301T000000Z');
    });
  });

  describe('alerts', () => {
    it('parses "alert N min before"', () => {
      const parsed = parseQuickAdd('standup alert 5 min before', REF)!;
      expect(parsed.title).toBe('standup');
      expect(parsed.reminderMinutes).toEqual([5]);
    });

    it('parses "alert N minutes" without a trailing "before"', () => {
      const parsed = parseQuickAdd('standup alert 10 minutes', REF)!;
      expect(parsed.reminderMinutes).toEqual([10]);
    });

    it('parses "remind me N hour before" converting to minutes', () => {
      const parsed = parseQuickAdd('call remind me 1 hour before', REF)!;
      expect(parsed.reminderMinutes).toEqual([60]);
    });

    it('parses "reminder N hours early" converting to minutes', () => {
      const parsed = parseQuickAdd('call reminder 2 hours early', REF)!;
      expect(parsed.reminderMinutes).toEqual([120]);
    });

    it('parses "notify me N day before" converting to minutes', () => {
      const parsed = parseQuickAdd('call notify me 1 day before', REF)!;
      expect(parsed.reminderMinutes).toEqual([1440]);
    });

    it('collects multiple alert phrases in order', () => {
      const parsed = parseQuickAdd('call alert 10 min before alert 1 hour before', REF)!;
      expect(parsed.reminderMinutes).toEqual([10, 60]);
    });

    it('does not treat a bare "reminder" (no number) as an alert', () => {
      const parsed = parseQuickAdd('meeting reminder', REF)!;
      expect(parsed.reminderMinutes).toEqual([]);
      expect(parsed.title).toBe('meeting reminder');
    });
  });

  describe('duration words', () => {
    it('parses "for half a hour"', () => {
      const parsed = parseQuickAdd('workshop for half a hour', REF)!;
      expect(parsed.title).toBe('workshop');
      expect(parsed.durationMinutes).toBe(30);
      expect(hm(parsed.end)).toBe(hm(new Date(parsed.start.getTime() + 30 * 60 * 1000)));
    });

    it('parses "for half an hour"', () => {
      const parsed = parseQuickAdd('workshop for half an hour', REF)!;
      expect(parsed.durationMinutes).toBe(30);
    });

    it('parses "for an hour"', () => {
      const parsed = parseQuickAdd('workshop for an hour', REF)!;
      expect(parsed.durationMinutes).toBe(60);
    });

    it('parses "for an hour and a half"', () => {
      const parsed = parseQuickAdd('workshop for an hour and a half', REF)!;
      expect(parsed.durationMinutes).toBe(90);
    });

    it('still exposes durationMinutes for the existing numeric duration rule', () => {
      const parsed = parseQuickAdd('standup for 45 min', REF)!;
      expect(parsed.durationMinutes).toBe(45);
    });

    // Pin: DURATION_WORD_RE requires "half a/an hour" (the article between
    // "half" and "hour"); the more natural "for a half hour" phrasing has no
    // article there, so it is left unrecognized. Documenting this known gap
    // rather than silently leaving it uncovered.
    it('does not recognize "for a half hour" (known-unsupported duration phrasing)', () => {
      const parsed = parseQuickAdd('Coffee tomorrow 3pm for a half hour', REF)!;
      expect(parsed.durationMinutes).toBeNull();
      expect(hm(parsed.end)).toBe(hm(new Date(parsed.start.getTime() + 60 * 60 * 1000)));
    });
  });

  describe('attendees', () => {
    it('surfaces a capitalized name as a suggestion without consuming it from the title', () => {
      const parsed = parseQuickAdd('lunch with Ana tomorrow', REF)!;
      expect(parsed.title).toBe('lunch with Ana');
      expect(parsed.attendeeNames).toEqual(['Ana']);
      expect(parsed.attendeeEmails).toEqual([]);
    });

    it('consumes an email from the title into attendeeEmails', () => {
      const parsed = parseQuickAdd(
        'lunch with ana@acme.com tomorrow 12:30-1:30 at Blue Bottle',
        REF,
      )!;
      expect(parsed.title).toBe('lunch');
      expect(parsed.attendeeEmails).toEqual(['ana@acme.com']);
      expect(parsed.attendeeNames).toEqual([]);
      expect(parsed.location).toBe('Blue Bottle');
      expect(hm(parsed.start)).toBe('2024-1-11 12:30');
      expect(hm(parsed.end)).toBe('2024-1-11 13:30');
    });

    it('splits multiple names joined by "and"', () => {
      const parsed = parseQuickAdd('meeting with Sam and Jill at 3', REF)!;
      expect(parsed.title).toBe('meeting with Sam and Jill');
      expect(parsed.attendeeNames).toEqual(['Sam', 'Jill']);
      expect(parsed.attendeeEmails).toEqual([]);
    });

    it('keeps a name in the title while stripping a mixed-in email', () => {
      const parsed = parseQuickAdd('sync with bob@co.com and Ana at 2', REF)!;
      expect(parsed.title).toBe('sync with Ana');
      expect(parsed.attendeeEmails).toEqual(['bob@co.com']);
      expect(parsed.attendeeNames).toEqual(['Ana']);
    });
  });

  describe('location', () => {
    it('parses a location after time has already consumed the first "at"', () => {
      const parsed = parseQuickAdd('call at 3 at Office', REF)!;
      expect(parsed.title).toBe('call');
      expect(parsed.location).toBe('Office');
      expect(hm(parsed.start)).toBe('2024-1-10 15:00');
    });

    it('parses an "@" location', () => {
      const parsed = parseQuickAdd('team offsite @ Lake Tahoe', REF)!;
      expect(parsed.title).toBe('team offsite');
      expect(parsed.location).toBe('Lake Tahoe');
    });

    it('stops the location capture at a trailing "for" clause', () => {
      const parsed = parseQuickAdd('meeting at Blue Bottle for the team', REF)!;
      expect(parsed.location).toBe('Blue Bottle');
    });

    it('stops the location capture at a trailing "with" clause', () => {
      const parsed = parseQuickAdd('dinner at Nobu with Ana', REF)!;
      expect(parsed.location).toBe('Nobu');
      expect(parsed.attendeeNames).toEqual(['Ana']);
      expect(parsed.title).toBe('dinner with Ana');
    });

    it('rejects a purely numeric capture', () => {
      const parsed = parseQuickAdd('task in 5', REF)!;
      expect(parsed.location).toBeNull();
      expect(parsed.title).toBe('task in 5');
    });

    it('does not treat "dinner at 7" as having a location (time already consumed the "at")', () => {
      const parsed = parseQuickAdd('dinner at 7', REF)!;
      expect(parsed.location).toBeNull();
      expect(hm(parsed.start)).toBe('2024-1-10 19:00');
    });
  });

  describe('field defaults', () => {
    it('defaults the new M2.2 fields when nothing extra is recognized', () => {
      const parsed = parseQuickAdd('Buy milk', REF)!;
      expect(parsed.location).toBeNull();
      expect(parsed.recurrenceRule).toBeNull();
      expect(parsed.reminderMinutes).toEqual([]);
      expect(parsed.attendeeEmails).toEqual([]);
      expect(parsed.attendeeNames).toEqual([]);
      expect(parsed.durationMinutes).toBeNull();
    });
  });

  describe('combinations', () => {
    it('parses recurrence + time + alert together', () => {
      const parsed = parseQuickAdd('standup every weekday 9:30 alert 5 min before', REF)!;
      expect(parsed.title).toBe('standup');
      expect(parsed.recurrenceRule).toBe('FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR');
      expect(parsed.reminderMinutes).toEqual([5]);
      expect(hm(parsed.start)).toBe('2024-1-15 9:30');
    });

    it('parses recurrence + time + duration + attendee name together', () => {
      const parsed = parseQuickAdd('1:1 with Sarah every other tuesday at 2pm for 45 min', REF)!;
      expect(parsed.title).toBe('1:1 with Sarah');
      expect(parsed.recurrenceRule).toBe('FREQ=WEEKLY;INTERVAL=2;BYDAY=TU');
      expect(parsed.attendeeNames).toEqual(['Sarah']);
      expect(parsed.durationMinutes).toBe(45);
      expect(hm(parsed.start)).toBe('2024-1-16 14:00');
      expect(hm(parsed.end)).toBe('2024-1-16 14:45');
    });

    it('parses monthly recurrence combined with an unsupported ordinal day-of-month (left in the title)', () => {
      const parsed = parseQuickAdd('rent reminder monthly on the 1st', REF)!;
      expect(parsed.recurrenceRule).toBe('FREQ=MONTHLY');
      expect(parsed.title).toBe('rent reminder on the 1st');
    });
  });

  describe('hasExplicitTime', () => {
    it('is true for a bare hour range with no am/pm ( "call 9-10")', () => {
      const parsed = parseQuickAdd('call 9-10', REF)!;
      expect(parsed.hasExplicitTime).toBe(true);
    });

    it('is false when only a date, no time, was recognized ("lunch tomorrow")', () => {
      const parsed = parseQuickAdd('lunch tomorrow', REF)!;
      expect(parsed.hasExplicitTime).toBe(false);
    });

    it('is true for an explicit "at <hour>" time ("standup at 9")', () => {
      const parsed = parseQuickAdd('standup at 9', REF)!;
      expect(parsed.hasExplicitTime).toBe(true);
    });

    it('is false when nothing at all was recognized', () => {
      const parsed = parseQuickAdd('Buy milk', REF)!;
      expect(parsed.hasExplicitTime).toBe(false);
    });
  });
});

describe('nextHalfHour', () => {
  it('rounds up to :30 when currently before the half hour', () => {
    const ref = new Date(2024, 0, 10, 9, 10, 0, 0);
    expect(hm(nextHalfHour(ref))).toBe('2024-1-10 9:30');
  });

  it('rounds up to the next hour when currently past the half hour', () => {
    const ref = new Date(2024, 0, 10, 9, 45, 0, 0);
    expect(hm(nextHalfHour(ref))).toBe('2024-1-10 10:00');
  });

  it('rounds up to the next hour when exactly on the half hour', () => {
    const ref = new Date(2024, 0, 10, 9, 30, 0, 0);
    expect(hm(nextHalfHour(ref))).toBe('2024-1-10 10:00');
  });

  it('defaults to the current time when no ref is passed', () => {
    const before = Date.now();
    const result = nextHalfHour();
    expect(result.getTime()).toBeGreaterThanOrEqual(before);
  });
});
