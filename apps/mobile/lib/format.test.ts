import {
  addDays,
  dayKey,
  endOfDay,
  formatDate,
  formatDayTitle,
  formatTime,
  formatTimeRange,
  isSameDay,
  relativeTime,
  startOfDay,
  startOfWeek,
} from './format';

// Fixed "now" so relativeTime is fully deterministic (no Date.now() reliance).
const NOW = new Date('2026-01-15T12:00:00.000Z').getTime();
const nowIso = (deltaMs: number) => new Date(NOW - deltaMs).toISOString();

describe('relativeTime', () => {
  it('returns "now" for anything under ~30s ago (rounds to 0 minutes)', () => {
    expect(relativeTime(nowIso(0), NOW)).toBe('now');
    expect(relativeTime(nowIso(29_000), NOW)).toBe('now');
  });

  it('returns minutes for 30s..59m59s (rounds to nearest minute)', () => {
    expect(relativeTime(nowIso(30_000), NOW)).toBe('1m'); // rounds up at the 30s boundary
    expect(relativeTime(nowIso(60_000), NOW)).toBe('1m');
    expect(relativeTime(nowIso(59 * 60_000), NOW)).toBe('59m');
  });

  it('rolls over to hours at exactly 60 minutes, not "60m"', () => {
    expect(relativeTime(nowIso(60 * 60_000), NOW)).toBe('1h');
    expect(relativeTime(nowIso(23 * 60 * 60_000), NOW)).toBe('23h');
  });

  it('rolls over to days at exactly 24 hours, not "24h"', () => {
    expect(relativeTime(nowIso(24 * 60 * 60_000), NOW)).toBe('1d');
    expect(relativeTime(nowIso(6 * 24 * 60 * 60_000), NOW)).toBe('6d');
  });

  it('falls back to a formatted date at exactly 7 days, not "7d"', () => {
    const result = relativeTime(nowIso(7 * 24 * 60 * 60_000), NOW);
    expect(result).not.toBe('7d');
    // "Mon D" style short date, e.g. "Jan 8" — locale-formatted, so match loosely.
    expect(result).toMatch(/^[A-Za-z]{3}\s\d{1,2}$/);
  });

  it('defaults `now` to Date.now() when omitted', () => {
    const spy = jest.spyOn(Date, 'now').mockReturnValue(NOW);
    try {
      expect(relativeTime(nowIso(0))).toBe('now');
    } finally {
      spy.mockRestore();
    }
  });
});

describe('formatTime / formatTimeRange / formatDate / formatDayTitle', () => {
  const iso = '2026-03-04T09:05:00.000Z';
  const iso2 = '2026-03-04T10:30:00.000Z';

  it('formatTime renders an hour:minute clock string', () => {
    expect(formatTime(iso)).toMatch(/^\d{1,2}:\d{2}\s?(AM|PM)?$/i);
  });

  it('formatTimeRange joins two formatted times with an en dash', () => {
    const range = formatTimeRange(iso, iso2);
    expect(range).toBe(`${formatTime(iso)} – ${formatTime(iso2)}`);
    expect(range).toContain('–');
  });

  it('formatDate renders month, day, and year', () => {
    const result = formatDate(iso);
    expect(result).toMatch(/\d{4}/); // contains the year
    expect(result).toMatch(/[A-Za-z]{3}/); // contains a short month name
  });

  it('formatDayTitle renders a weekday + short date', () => {
    const result = formatDayTitle(new Date(2026, 2, 4)); // local Mar 4 2026 (Wed)
    expect(result).toMatch(/[A-Za-z]{3}.*[A-Za-z]{3}\s\d{1,2}/);
  });
});

describe('startOfDay', () => {
  it('zeroes the time-of-day, keeping the calendar date', () => {
    const input = new Date(2026, 5, 15, 14, 37, 22, 123);
    const result = startOfDay(input);
    expect(result.getFullYear()).toBe(2026);
    expect(result.getMonth()).toBe(5);
    expect(result.getDate()).toBe(15);
    expect(result.getHours()).toBe(0);
    expect(result.getMinutes()).toBe(0);
    expect(result.getSeconds()).toBe(0);
    expect(result.getMilliseconds()).toBe(0);
  });

  it('does not mutate the input date', () => {
    const input = new Date(2026, 5, 15, 14, 37, 22, 123);
    const before = input.getTime();
    startOfDay(input);
    expect(input.getTime()).toBe(before);
  });
});

describe('endOfDay', () => {
  it('sets the time-of-day to the last millisecond, keeping the calendar date', () => {
    const input = new Date(2026, 5, 15, 3, 0, 0, 0);
    const result = endOfDay(input);
    expect(result.getFullYear()).toBe(2026);
    expect(result.getMonth()).toBe(5);
    expect(result.getDate()).toBe(15);
    expect(result.getHours()).toBe(23);
    expect(result.getMinutes()).toBe(59);
    expect(result.getSeconds()).toBe(59);
    expect(result.getMilliseconds()).toBe(999);
  });
});

describe('addDays', () => {
  it('adds a positive number of days', () => {
    const result = addDays(new Date(2026, 0, 15), 3);
    expect(result.getFullYear()).toBe(2026);
    expect(result.getMonth()).toBe(0);
    expect(result.getDate()).toBe(18);
  });

  it('subtracts with a negative number of days', () => {
    const result = addDays(new Date(2026, 0, 15), -20);
    expect(result.getFullYear()).toBe(2025);
    expect(result.getMonth()).toBe(11); // December
    expect(result.getDate()).toBe(26);
  });

  it('rolls over a month boundary', () => {
    const result = addDays(new Date(2026, 0, 31), 1); // Jan 31 -> Feb 1
    expect(result.getMonth()).toBe(1);
    expect(result.getDate()).toBe(1);
  });

  it('rolls over a year boundary', () => {
    const result = addDays(new Date(2025, 11, 31), 1); // Dec 31 2025 -> Jan 1 2026
    expect(result.getFullYear()).toBe(2026);
    expect(result.getMonth()).toBe(0);
    expect(result.getDate()).toBe(1);
  });

  it('handles the Feb 29 leap-day boundary', () => {
    const result = addDays(new Date(2024, 1, 29), 1); // 2024 is a leap year
    expect(result.getFullYear()).toBe(2024);
    expect(result.getMonth()).toBe(2); // March
    expect(result.getDate()).toBe(1);
  });

  it('adding zero days returns an equal (but distinct) date', () => {
    const input = new Date(2026, 0, 15, 9, 30);
    const result = addDays(input, 0);
    expect(result.getTime()).toBe(input.getTime());
    expect(result).not.toBe(input);
  });
});

describe('isSameDay', () => {
  it('is true for two times on the same calendar day', () => {
    expect(isSameDay(new Date(2026, 0, 15, 0, 0, 0), new Date(2026, 0, 15, 23, 59, 59))).toBe(
      true
    );
  });

  it('is false just across a midnight boundary', () => {
    expect(
      isSameDay(new Date(2026, 0, 15, 23, 59, 59, 999), new Date(2026, 0, 16, 0, 0, 0, 0))
    ).toBe(false);
  });

  it('is false for the same day-of-month in a different month', () => {
    expect(isSameDay(new Date(2026, 0, 15), new Date(2026, 1, 15))).toBe(false);
  });

  it('is false for the same month/day in a different year', () => {
    expect(isSameDay(new Date(2025, 0, 15), new Date(2026, 0, 15))).toBe(false);
  });
});

describe('dayKey', () => {
  it('formats as zero-padded yyyy-mm-dd', () => {
    expect(dayKey(new Date(2026, 0, 5))).toBe('2026-01-05');
  });

  it('zero-pads December and day 25', () => {
    expect(dayKey(new Date(2026, 11, 25))).toBe('2026-12-25');
  });

  it('is stable regardless of time-of-day', () => {
    expect(dayKey(new Date(2026, 5, 1, 23, 59, 59))).toBe('2026-06-01');
  });
});

describe('startOfWeek', () => {
  // Reference week: Mon 2026-01-05 .. Sun 2026-01-11.
  it('returns the same (zeroed) date when given a Monday', () => {
    const result = startOfWeek(new Date(2026, 0, 5, 18, 0));
    expect(dayKey(result)).toBe('2026-01-05');
    expect(result.getHours()).toBe(0);
  });

  it('returns the preceding Monday for a mid-week date', () => {
    const result = startOfWeek(new Date(2026, 0, 7, 9, 0)); // Wednesday
    expect(dayKey(result)).toBe('2026-01-05');
  });

  it('returns the preceding Monday for a Sunday (end of week, not start of next)', () => {
    const result = startOfWeek(new Date(2026, 0, 11, 23, 0)); // Sunday
    expect(dayKey(result)).toBe('2026-01-05');
  });

  it('rolls back across a month/year boundary', () => {
    // Sunday 2026-01-04 belongs to the week starting Monday 2025-12-29.
    const result = startOfWeek(new Date(2026, 0, 4));
    expect(dayKey(result)).toBe('2025-12-29');
  });
});
