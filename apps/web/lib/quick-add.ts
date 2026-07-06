import { addDays, addMinutes, isSameDay, startOfDay } from 'date-fns';

/**
 * Lightweight natural-language parser for the calendar quick-add input.
 * Handles inputs like:
 *   "lunch with Ana tomorrow 12:30-1:30"
 *   "standup monday at 9:30"
 *   "dentist 7/24 11am for 45 min"
 * Deliberately heuristic - the result prefills the event dialog, so the user
 * always confirms before anything is created.
 */

export interface QuickAddParse {
  title: string;
  start: Date;
  end: Date;
  allDay: boolean;
  /** True when an explicit date and/or time was recognized in the text. */
  matched: boolean;
}

const WEEKDAY_PREFIXES = ['sun', 'mon', 'tue', 'wed', 'thu', 'fri', 'sat'];
const MONTH_PREFIXES = [
  'jan', 'feb', 'mar', 'apr', 'may', 'jun',
  'jul', 'aug', 'sep', 'oct', 'nov', 'dec',
];

const RANGE_RE =
  /\b(\d{1,2})(?::(\d{2}))?\s*(am|pm)?\s*(?:-|–|—|to|until)\s*(\d{1,2})(?::(\d{2}))?\s*(am|pm)?\b/i;
const AT_TIME_RE = /\bat\s+(\d{1,2})(?::(\d{2}))?\s*(am|pm)?\b/i;
const CLOCK_TIME_RE = /\b(\d{1,2}):(\d{2})\s*(am|pm)?\b/i;
const MERIDIEM_TIME_RE = /\b(\d{1,2})\s*(am|pm)\b/i;
const WORD_TIME_RE = /\b(noon|midday|midnight)\b/i;
const DURATION_RE = /\bfor\s+(\d+(?:\.\d+)?)\s*(hours?|hrs?|hr|h|minutes?|mins?|min|m)\b/i;
const TODAY_RE = /\b(today|tonight)\b/i;
const TOMORROW_RE = /\b(tomorrow|tmrw?)\b/i;
const WEEKDAY_RE =
  /\b(?:next\s+|this\s+)?(sunday|sun|monday|mon|tuesday|tues|tue|wednesday|wed|thursday|thurs|thu|friday|fri|saturday|sat)\b/i;
const MONTH_DAY_RE =
  /\b(january|jan|february|feb|march|mar|april|apr|may|june|jun|july|jul|august|aug|september|sept|sep|october|oct|november|nov|december|dec)\.?\s+(\d{1,2})(?:st|nd|rd|th)?\b/i;
const NUMERIC_DATE_RE = /\b(\d{1,2})\/(\d{1,2})(?:\/(\d{2,4}))?\b/;

function to24(hour: number, meridiem: string | undefined): number {
  let h = hour % 24;
  if (meridiem === 'pm' && h < 12) h += 12;
  if (meridiem === 'am' && h === 12) h = 0;
  return h;
}

/** "1-7 with no am/pm means PM" heuristic used by most quick-add parsers. */
function resolveSingle(hour: number, minute: number, meridiem: string | undefined): number {
  let v = to24(hour, meridiem?.toLowerCase()) * 60 + minute;
  if (!meridiem && v < 8 * 60) v += 720;
  return v;
}

/** Next :00 or :30 boundary after `ref`. */
export function nextHalfHour(ref: Date = new Date()): Date {
  const d = new Date(ref);
  d.setMinutes(d.getMinutes() < 30 ? 30 : 60, 0, 0);
  return d;
}

export function parseQuickAdd(raw: string, ref: Date = new Date()): QuickAddParse | null {
  const trimmed = raw.trim();
  if (!trimmed) return null;

  let text = ` ${trimmed} `;
  let matched = false;

  const consume = (re: RegExp): RegExpExecArray | null => {
    const m = re.exec(text);
    if (!m) return null;
    text = `${text.slice(0, m.index)} ${text.slice(m.index + m[0].length)}`;
    matched = true;
    return m;
  };

  // --- time range ("12:30-1:30", "2 to 3pm") ---
  let rangeStart: number | null = null;
  let rangeEnd: number | null = null;
  const range = consume(RANGE_RE);
  if (range) {
    const sh = Number(range[1]);
    const sm = Number(range[2] ?? '0');
    const eh = Number(range[4]);
    const em = Number(range[5] ?? '0');
    const sMer = range[3]?.toLowerCase();
    const eMer = range[6]?.toLowerCase();

    const e24 = to24(eh, eMer) * 60 + em;
    let s24 = to24(sh, sMer) * 60 + sm;
    if (!sMer && eMer) {
      const borrowed = to24(sh, eMer) * 60 + sm;
      if (borrowed < e24) s24 = borrowed; // "12:30-1:30pm" -> starts 12:30pm
    }
    let end24 = e24;
    if (end24 <= s24) end24 += 720; // "12:30-1:30" -> ends 13:30
    if (end24 <= s24) end24 = s24 + 60;
    if (!sMer && !eMer && s24 < 8 * 60) {
      s24 += 720; // "3-4" -> 3pm-4pm
      end24 += 720;
    }
    rangeStart = s24;
    rangeEnd = end24;
  }

  // --- single time ("at 3", "9:15", "7pm", "noon") ---
  let singleMinutes: number | null = null;
  if (rangeStart === null) {
    const at = consume(AT_TIME_RE);
    if (at) {
      singleMinutes = resolveSingle(Number(at[1]), Number(at[2] ?? '0'), at[3]);
    } else {
      const clock = consume(CLOCK_TIME_RE);
      if (clock) {
        singleMinutes = resolveSingle(Number(clock[1]), Number(clock[2]), clock[3]);
      } else {
        const mer = consume(MERIDIEM_TIME_RE);
        if (mer) {
          singleMinutes = resolveSingle(Number(mer[1]), 0, mer[2]);
        } else {
          const word = consume(WORD_TIME_RE);
          if (word) singleMinutes = word[1].toLowerCase() === 'midnight' ? 0 : 12 * 60;
        }
      }
    }
  }

  // --- duration ("for 45 min", "for 2 hours") ---
  let durationMinutes: number | null = null;
  const dur = consume(DURATION_RE);
  if (dur) {
    const n = Number(dur[1]);
    durationMinutes = /^h/i.test(dur[2]) ? Math.round(n * 60) : Math.round(n);
  }

  // --- date ("today", "tomorrow", "friday", "jul 24", "7/24") ---
  let day: Date | null = null;
  let isTonight = false;
  const today = consume(TODAY_RE);
  if (today) {
    day = startOfDay(ref);
    isTonight = today[1].toLowerCase() === 'tonight';
  } else if (consume(TOMORROW_RE)) {
    day = startOfDay(addDays(ref, 1));
  } else {
    const wd = consume(WEEKDAY_RE);
    if (wd) {
      const target = WEEKDAY_PREFIXES.indexOf(wd[1].slice(0, 3).toLowerCase());
      let diff = (target - ref.getDay() + 7) % 7;
      if (diff === 0) diff = 7; // upcoming occurrence, never today
      day = startOfDay(addDays(ref, diff));
    } else {
      const md = consume(MONTH_DAY_RE);
      if (md) {
        const month = MONTH_PREFIXES.indexOf(md[1].slice(0, 3).toLowerCase());
        let candidate = new Date(ref.getFullYear(), month, Number(md[2]));
        if (candidate < startOfDay(ref)) {
          candidate = new Date(ref.getFullYear() + 1, month, Number(md[2]));
        }
        day = startOfDay(candidate);
      } else {
        const nd = consume(NUMERIC_DATE_RE);
        if (nd) {
          const month = Number(nd[1]) - 1;
          const dd = Number(nd[2]);
          let year = nd[3] ? Number(nd[3]) : ref.getFullYear();
          if (year < 100) year += 2000;
          let candidate = new Date(year, month, dd);
          if (!nd[3] && candidate < startOfDay(ref)) candidate = new Date(year + 1, month, dd);
          day = startOfDay(candidate);
        }
      }
    }
  }

  // --- assemble ---
  const base = day ?? startOfDay(ref);
  let start: Date;
  let end: Date;

  if (rangeStart !== null && rangeEnd !== null) {
    start = addMinutes(base, rangeStart);
    end = addMinutes(base, rangeEnd);
  } else if (singleMinutes !== null) {
    start = addMinutes(base, singleMinutes);
    end = addMinutes(start, durationMinutes ?? 60);
  } else if (day !== null) {
    start = addMinutes(base, isTonight ? 19 * 60 : 9 * 60);
    if (isSameDay(base, ref) && start < ref) start = nextHalfHour(ref);
    end = addMinutes(start, durationMinutes ?? 60);
  } else {
    start = nextHalfHour(ref);
    end = addMinutes(start, durationMinutes ?? 60);
  }

  // --- title = whatever is left ---
  let title = text.replace(/\s+/g, ' ').trim();
  title = title
    .replace(/^(?:on|at|from|in)\s+/i, '')
    .replace(/\s+(?:on|at|from|in)$/i, '')
    .replace(/^[,\s-]+|[,\s-]+$/g, '')
    .trim();

  return {
    title: title || 'New event',
    start,
    end,
    allDay: false,
    matched,
  };
}
