import {
  addDays,
  addMonths,
  addQuarters,
  addYears,
  eachDayOfInterval,
  endOfMonth,
  format,
  isSameMonth,
  startOfDay,
  startOfMonth,
  startOfQuarter,
  startOfWeek,
  startOfYear,
} from 'date-fns';

export type CalendarView = 'day' | 'week' | 'month' | 'quarter' | 'year' | 'ticker';

export const WEEK_OPTS = { weekStartsOn: 0 as const };

/** Rolling window length (days) for the ticker view — the agenda list's engine. */
export const TICKER_DAYS = 30;

export interface ViewRange {
  /** Inclusive start of the range. */
  from: Date;
  /** Exclusive end of the range — feeds GET /v1/events from/to directly. */
  to: Date;
  /** Grid days for day/week/month; [] for quarter/year/ticker (those render MiniMonth grids or a rolling list instead). */
  days: Date[];
}

export function viewRange(view: CalendarView, anchor: Date): ViewRange {
  switch (view) {
    case 'day': {
      const from = startOfDay(anchor);
      return { from, to: addDays(from, 1), days: [from] };
    }
    case 'week': {
      const from = startOfWeek(anchor, WEEK_OPTS);
      return { from, to: addDays(from, 7), days: [...Array(7)].map((_, i) => addDays(from, i)) };
    }
    case 'month': {
      // Full leading/trailing weeks: 4–6 rows × 7, always whole weeks.
      const from = startOfWeek(startOfMonth(anchor), WEEK_OPTS);
      const to = addDays(startOfWeek(endOfMonth(anchor), WEEK_OPTS), 7);
      return { from, to, days: eachDayOfInterval({ start: from, end: addDays(to, -1) }) };
    }
    case 'quarter': {
      const from = startOfQuarter(anchor);
      return { from, to: addQuarters(from, 1), days: [] }; // renders 3 MiniMonth grids
    }
    case 'year': {
      const from = startOfYear(anchor);
      return { from, to: addYears(from, 1), days: [] }; // renders 12 MiniMonth grids
    }
    case 'ticker': {
      const from = startOfDay(anchor);
      return { from, to: addDays(from, TICKER_DAYS), days: [] };
    }
  }
}

/** J/K step per view: day/ticker ±1d, week ±7d, month ±1mo, quarter ±3mo, year ±1y. */
export function stepAnchor(view: CalendarView, anchor: Date, dir: 1 | -1): Date {
  switch (view) {
    case 'day':
    case 'ticker':
      return addDays(anchor, dir);
    case 'week':
      return addDays(anchor, dir * 7);
    case 'month':
      return addMonths(anchor, dir);
    case 'quarter':
      return addQuarters(anchor, dir);
    case 'year':
      return addYears(anchor, dir);
  }
}

/** Header label per view, e.g. "Jul 17, 2026", "Jul 13 – 19, 2026", "July 2026", "Q3 2026", "2026". */
export function rangeLabel(view: CalendarView, anchor: Date): string {
  switch (view) {
    case 'day':
    case 'ticker':
      return format(anchor, 'MMM d, yyyy');
    case 'week': {
      const from = startOfWeek(anchor, WEEK_OPTS);
      const to = addDays(from, 6);
      return isSameMonth(from, to)
        ? `${format(from, 'MMM d')} – ${format(to, 'd, yyyy')}`
        : `${format(from, 'MMM d')} – ${format(to, 'MMM d, yyyy')}`;
    }
    case 'month':
      return format(anchor, 'MMMM yyyy');
    case 'quarter': {
      const quarter = Math.floor(anchor.getMonth() / 3) + 1;
      return `Q${quarter} ${anchor.getFullYear()}`;
    }
    case 'year':
      return format(anchor, 'yyyy');
  }
}

/** Single-key view switch map used by shortcuts + palette. */
export const VIEW_KEYS = {
  d: 'day',
  w: 'week',
  m: 'month',
  q: 'quarter',
  y: 'year',
  a: 'ticker',
} as const;
