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
