import { addDays, addMonths, addQuarters, addYears } from 'date-fns';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';

import {
  TICKER_DAYS,
  VIEW_KEYS,
  WEEK_OPTS,
  rangeLabel,
  stepAnchor,
  viewRange,
} from '@/lib/calendar-views';

describe('viewRange', () => {
  describe('month grid', () => {
    it('spans exactly 4 whole weeks when the 1st falls on the week start (Feb 2026)', () => {
      // Feb 1, 2026 is a Sunday — a month that needs no leading days at all.
      const { days, from, to } = viewRange('month', new Date(2026, 1, 15));
      expect(from.getDay()).toBe(0);
      expect(days.length).toBe(28);
      expect(to.getTime() - from.getTime()).toBe(28 * 24 * 60 * 60 * 1000);
    });

    it('spans 6 whole weeks for a month that straddles both ends (Aug 2026)', () => {
      const { days } = viewRange('month', new Date(2026, 7, 15));
      expect(days.length).toBe(42);
    });

    it('always yields a multiple of 7 grid days', () => {
      for (let month = 0; month < 12; month++) {
        const { days } = viewRange('month', new Date(2026, month, 10));
        expect(days.length % 7).toBe(0);
        expect(days.length).toBeGreaterThanOrEqual(28);
        expect(days.length).toBeLessThanOrEqual(42);
      }
    });
  });

  describe('to (exclusive) equals the next period\'s natural start', () => {
    it('day', () => {
      const anchor = new Date(2026, 6, 17);
      const { from, to } = viewRange('day', anchor);
      expect(to.getTime()).toBe(viewRange('day', addDays(anchor, 1)).from.getTime());
    });

    it('week', () => {
      const anchor = new Date(2026, 6, 17);
      const { to } = viewRange('week', anchor);
      expect(to.getTime()).toBe(viewRange('week', addDays(anchor, 7)).from.getTime());
    });

    it('month (day after the last grid day, keeping whole trailing weeks)', () => {
      const anchor = new Date(2026, 6, 17);
      const { to, days } = viewRange('month', anchor);
      const lastGridDay = days[days.length - 1];
      expect(to.getTime()).toBe(addDays(lastGridDay, 1).getTime());
    });

    it('quarter', () => {
      const anchor = new Date(2026, 6, 17);
      const { to } = viewRange('quarter', anchor);
      expect(to.getTime()).toBe(viewRange('quarter', addQuarters(anchor, 1)).from.getTime());
    });

    it('year', () => {
      const anchor = new Date(2026, 6, 17);
      const { to } = viewRange('year', anchor);
      expect(to.getTime()).toBe(viewRange('year', addYears(anchor, 1)).from.getTime());
    });

    it('ticker spans TICKER_DAYS days', () => {
      const anchor = new Date(2026, 6, 17);
      const { from, to } = viewRange('ticker', anchor);
      expect(to.getTime()).toBe(addDays(from, TICKER_DAYS).getTime());
    });
  });

  describe('quarter boundaries', () => {
    it('Jul 17 2026 falls in Q3: Jul 1 - Oct 1', () => {
      const { from, to } = viewRange('quarter', new Date(2026, 6, 17));
      expect(from).toEqual(new Date(2026, 6, 1));
      expect(to).toEqual(new Date(2026, 9, 1));
    });
  });

  describe('DST-crossing week', () => {
    let originalTz: string | undefined;

    beforeEach(() => {
      originalTz = process.env.TZ;
      process.env.TZ = 'America/New_York';
    });

    afterEach(() => {
      process.env.TZ = originalTz;
    });

    it('keeps 7 days across the US spring-forward transition (Mar 8, 2026)', () => {
      const { days } = viewRange('week', new Date(2026, 2, 10));
      expect(days.length).toBe(7);
      expect(days[0].getDate()).toBe(8);
      expect(days[6].getDate()).toBe(14);
      // Every grid day should still land at local midnight, not drift an
      // hour off because of the DST jump.
      for (const day of days) expect(day.getHours()).toBe(0);
    });

    it('keeps 7 days across the US fall-back transition (Nov 1, 2026)', () => {
      const { days } = viewRange('week', new Date(2026, 10, 3));
      expect(days.length).toBe(7);
      for (const day of days) expect(day.getHours()).toBe(0);
    });
  });

  it('week always starts on Sunday per WEEK_OPTS', () => {
    expect(WEEK_OPTS.weekStartsOn).toBe(0);
    const { from } = viewRange('week', new Date(2026, 6, 17));
    expect(from.getDay()).toBe(0);
  });
});

describe('stepAnchor', () => {
  const anchor = new Date(2026, 6, 15, 9, 30); // mid-month, avoids month-length edge cases

  it.each(['day', 'week', 'month', 'quarter', 'year', 'ticker'] as const)(
    'round-trips forward then back for %s',
    (view) => {
      const stepped = stepAnchor(view, anchor, 1);
      const back = stepAnchor(view, stepped, -1);
      expect(back.getTime()).toBe(anchor.getTime());
    }
  );

  it('steps day/ticker by 1 day', () => {
    expect(stepAnchor('day', anchor, 1).getTime()).toBe(addDays(anchor, 1).getTime());
    expect(stepAnchor('ticker', anchor, 1).getTime()).toBe(addDays(anchor, 1).getTime());
  });

  it('steps week by 7 days', () => {
    expect(stepAnchor('week', anchor, 1).getTime()).toBe(addDays(anchor, 7).getTime());
  });

  it('steps month by 1 month', () => {
    expect(stepAnchor('month', anchor, 1).getTime()).toBe(addMonths(anchor, 1).getTime());
  });

  it('steps quarter by 3 months', () => {
    expect(stepAnchor('quarter', anchor, 1).getTime()).toBe(addQuarters(anchor, 1).getTime());
  });

  it('steps year by 1 year', () => {
    expect(stepAnchor('year', anchor, 1).getTime()).toBe(addYears(anchor, 1).getTime());
  });
});

describe('rangeLabel', () => {
  it('day', () => {
    expect(rangeLabel('day', new Date(2026, 6, 17))).toBe('Jul 17, 2026');
  });

  it('week within the same month', () => {
    // Sunday Jul 12 - Saturday Jul 18, 2026 (weekStartsOn: 0).
    expect(rangeLabel('week', new Date(2026, 6, 15))).toBe('Jul 12 – 18, 2026');
  });

  it('week spanning two months', () => {
    // Sunday Jun 28 - Saturday Jul 4, 2026.
    expect(rangeLabel('week', new Date(2026, 5, 30))).toBe('Jun 28 – Jul 4, 2026');
  });

  it('month', () => {
    expect(rangeLabel('month', new Date(2026, 6, 17))).toBe('July 2026');
  });

  it('quarter', () => {
    expect(rangeLabel('quarter', new Date(2026, 6, 17))).toBe('Q3 2026');
  });

  it('year', () => {
    expect(rangeLabel('year', new Date(2026, 6, 17))).toBe('2026');
  });
});

describe('VIEW_KEYS', () => {
  it('maps single keys to every view', () => {
    expect(VIEW_KEYS).toEqual({
      d: 'day',
      w: 'week',
      m: 'month',
      q: 'quarter',
      y: 'year',
      a: 'ticker',
    });
  });
});
