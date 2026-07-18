import { addDays, addMinutes, format, isSameDay, startOfDay } from 'date-fns';

/**
 * Lightweight natural-language parser for the calendar quick-add input.
 * Handles inputs like:
 *   "lunch with Ana tomorrow 12:30-1:30"
 *   "standup monday at 9:30"
 *   "dentist 7/24 11am for 45 min"
 *   "standup every weekday 9:30 alert 5 min before"
 *   "1:1 with Sarah every other tuesday at 2pm for 45 min"
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
  /** Free-text location, e.g. "Blue Bottle". */
  location: string | null;
  /** RFC 5545 RRULE body, e.g. "FREQ=WEEKLY;BYDAY=MO,WE". */
  recurrenceRule: string | null;
  /** Alerts, in minutes before start. */
  reminderMinutes: number[];
  /** Email addresses consumed from the text. */
  attendeeEmails: string[];
  /** Capitalized name tokens surfaced as suggestions; NOT consumed from the title. */
  attendeeNames: string[];
  /** Explicit duration, when given (e.g. "for 45 min", "for an hour"). */
  durationMinutes: number | null;
  /**
   * True when a time-consuming grammar stage (range, "at <hour>", clock time,
   * meridiem time, or a time word like "noon") actually matched — as opposed
   * to only a date being recognized. Callers (e.g. QuickAddBar) use this to
   * decide whether to show free-slot suggestions instead of re-deriving the
   * same judgment with their own regex, which can disagree with the parser.
   */
  hasExplicitTime: boolean;
}

const WEEKDAY_PREFIXES = ['sun', 'mon', 'tue', 'wed', 'thu', 'fri', 'sat'];
const WEEKDAY_CODES = ['SU', 'MO', 'TU', 'WE', 'TH', 'FR', 'SA'];
const MONTH_PREFIXES = [
  'jan', 'feb', 'mar', 'apr', 'may', 'jun',
  'jul', 'aug', 'sep', 'oct', 'nov', 'dec',
];
const WEEKDAY_ALT =
  'sunday|sun|monday|mon|tuesday|tues|tue|wednesday|wed|thursday|thurs|thu|friday|fri|saturday|sat';

const RANGE_RE =
  /\b(\d{1,2})(?::(\d{2}))?\s*(am|pm)?\s*(?:-|–|—|to|until)\s*(\d{1,2})(?::(\d{2}))?\s*(am|pm)?\b/i;
const AT_TIME_RE = /\bat\s+(\d{1,2})(?::(\d{2}))?\s*(am|pm)?\b/i;
const CLOCK_TIME_RE = /\b(\d{1,2}):(\d{2})\s*(am|pm)?\b/i;
const MERIDIEM_TIME_RE = /\b(\d{1,2})\s*(am|pm)\b/i;
const WORD_TIME_RE = /\b(noon|midday|midnight)\b/i;
const DURATION_RE = /\bfor\s+(\d+(?:\.\d+)?)\s*(hours?|hrs?|hr|h|minutes?|mins?|min|m)\b/i;
const DURATION_WORD_RE =
  /\bfor\s+(?:an?\s+)?(half\s+an?\s+hour|hour(?:\s+and\s+a\s+half)?)\b/i;
const TODAY_RE = /\b(today|tonight)\b/i;
const TOMORROW_RE = /\b(tomorrow|tmrw?)\b/i;
const WEEKDAY_RE = new RegExp(`\\b(?:next\\s+|this\\s+)?(${WEEKDAY_ALT})\\b`, 'i');
const MONTH_DAY_RE =
  /\b(january|jan|february|feb|march|mar|april|apr|may|june|jun|july|jul|august|aug|september|sept|sep|october|oct|november|nov|december|dec)\.?\s+(\d{1,2})(?:st|nd|rd|th)?\b/i;
const NUMERIC_DATE_RE = /\b(\d{1,2})\/(\d{1,2})(?:\/(\d{2,4}))?\b/;

const ALL_DAY_RE = /\ball[\s-]?day\b/i;

const EVERY_OTHER_WEEK_RE = /\b(?:biweekly|every\s+other\s+week)\b/i;
const EVERY_OTHER_WEEKDAY_RE = new RegExp(`\\bevery\\s+other\\s+(${WEEKDAY_ALT})\\b`, 'i');
const EVERY_WEEKDAY_KEYWORD_RE = /\bevery\s+weekday\b/i;
const EVERY_N_UNIT_RE = /\bevery\s+(\d+)\s+(days?|weeks?|months?|years?)\b/i;
const EVERY_WEEKDAY_LIST_RE = new RegExp(
  `\\bevery\\s+((?:${WEEKDAY_ALT})s?(?:\\s*(?:,|and)\\s*(?:${WEEKDAY_ALT})s?)*)\\b`,
  'i',
);
const DAILY_RE = /\b(?:daily|every\s+day)\b/i;
const WEEKLY_RE = /\b(?:weekly|every\s+week)\b/i;
const MONTHLY_RE = /\b(?:monthly|every\s+month)\b/i;
const YEARLY_RE = /\b(?:yearly|annually|every\s+year)\b/i;
const UNTIL_RE = /\buntil\s+([a-z]+\.?(?:\s+\d{1,2}(?:st|nd|rd|th)?)?|\d{1,2}\/\d{1,2}(?:\/\d{2,4})?)\b/i;

const ALERT_RE =
  /\b(?:alert|remind(?:er)?(?:\s+me)?|notify(?:\s+me)?)\s+(\d+)\s*(minutes?|mins?|min|m|hours?|hrs?|hr|h|days?|d)\s*(?:before|prior|early)?\b/i;

const WITH_RE =
  /\bwith\s+((?:[\w.+-]+@[\w-]+\.[\w.-]+|[A-Z][\w'’-]*)(?:\s*(?:,|and|&|\+)\s*(?:[\w.+-]+@[\w-]+\.[\w.-]+|[A-Z][\w'’-]*))*)/;

// Note: "@" can't share a \b anchor with "at|in" - \b requires a word
// character on one side, but both a preceding space and "@" itself are
// non-word characters, so `\b@` never matches. Anchor the keyword forms with
// \b and let "@" stand on its own.
const LOCATION_RE =
  /(?:\bat\b|\bin\b|@)\s+(.+?)(?=\s+(?:with|every|for|alert|remind|notify|until|from)\b|\s*$)/i;

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

function weekdayIndex(token: string): number {
  return WEEKDAY_PREFIXES.indexOf(token.slice(0, 3).toLowerCase());
}

function alertUnitToMinutes(n: number, unit: string): number {
  const u = unit.toLowerCase();
  if (u.startsWith('h')) return n * 60;
  if (u.startsWith('d')) return n * 1440;
  return n;
}

interface DateMatch {
  date: Date;
  index: number;
  length: number;
  isTonight: boolean;
}

/** Finds the first recognized date token in `s` without mutating it. */
function findDate(s: string, ref: Date): DateMatch | null {
  let m = TODAY_RE.exec(s);
  if (m) {
    return {
      date: startOfDay(ref),
      index: m.index,
      length: m[0].length,
      isTonight: m[1].toLowerCase() === 'tonight',
    };
  }
  m = TOMORROW_RE.exec(s);
  if (m) {
    return { date: startOfDay(addDays(ref, 1)), index: m.index, length: m[0].length, isTonight: false };
  }
  m = WEEKDAY_RE.exec(s);
  if (m) {
    const target = weekdayIndex(m[1]);
    let diff = (target - ref.getDay() + 7) % 7;
    if (diff === 0) diff = 7; // upcoming occurrence, never today
    return {
      date: startOfDay(addDays(ref, diff)),
      index: m.index,
      length: m[0].length,
      isTonight: false,
    };
  }
  m = MONTH_DAY_RE.exec(s);
  if (m) {
    const month = MONTH_PREFIXES.indexOf(m[1].slice(0, 3).toLowerCase());
    let candidate = new Date(ref.getFullYear(), month, Number(m[2]));
    if (candidate < startOfDay(ref)) {
      candidate = new Date(ref.getFullYear() + 1, month, Number(m[2]));
    }
    return { date: startOfDay(candidate), index: m.index, length: m[0].length, isTonight: false };
  }
  m = NUMERIC_DATE_RE.exec(s);
  if (m) {
    const month = Number(m[1]) - 1;
    const dd = Number(m[2]);
    let year = m[3] ? Number(m[3]) : ref.getFullYear();
    if (year < 100) year += 2000;
    let candidate = new Date(year, month, dd);
    if (!m[3] && candidate < startOfDay(ref)) candidate = new Date(year + 1, month, dd);
    return { date: startOfDay(candidate), index: m.index, length: m[0].length, isTonight: false };
  }
  return null;
}

interface RecurrenceResult {
  rule: string;
  /** 0=Sun..6=Sat anchor weekday to use when no explicit date was given. */
  anchorWeekday: number | null;
}

/** Appends a trailing "until <date>" clause to `rule`, if present. */
function finalizeRecurrence(
  rule: string,
  anchorWeekday: number | null,
  consume: (re: RegExp) => RegExpExecArray | null,
  ref: Date,
): RecurrenceResult {
  let finalRule = rule;
  const until = consume(UNTIL_RE);
  if (until) {
    const dm = findDate(until[1], ref);
    if (dm) {
      finalRule += `;UNTIL=${format(dm.date, 'yyyyMMdd')}T000000Z`;
    }
  }
  return { rule: finalRule, anchorWeekday };
}

/** Parses the recurrence sub-grammar, consuming matched text out of `consume`'s source. */
function parseRecurrence(
  consume: (re: RegExp) => RegExpExecArray | null,
  ref: Date,
): RecurrenceResult | null {
  const otherWeekday = consume(EVERY_OTHER_WEEKDAY_RE);
  if (otherWeekday) {
    const idx = weekdayIndex(otherWeekday[1]);
    return finalizeRecurrence(`FREQ=WEEKLY;INTERVAL=2;BYDAY=${WEEKDAY_CODES[idx]}`, idx, consume, ref);
  }
  if (consume(EVERY_OTHER_WEEK_RE)) {
    return finalizeRecurrence('FREQ=WEEKLY;INTERVAL=2', null, consume, ref);
  }
  if (consume(EVERY_WEEKDAY_KEYWORD_RE)) {
    return finalizeRecurrence('FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR', 1, consume, ref); // anchor: Monday
  }
  const everyN = consume(EVERY_N_UNIT_RE);
  if (everyN) {
    const n = everyN[1];
    const unit = everyN[2].toLowerCase();
    const freq = unit.startsWith('day')
      ? 'DAILY'
      : unit.startsWith('week')
        ? 'WEEKLY'
        : unit.startsWith('month')
          ? 'MONTHLY'
          : 'YEARLY';
    return finalizeRecurrence(`FREQ=${freq};INTERVAL=${n}`, null, consume, ref);
  }
  const weekdayList = consume(EVERY_WEEKDAY_LIST_RE);
  if (weekdayList) {
    const tokens = weekdayList[1]
      .split(/\s*(?:,|and)\s*/i)
      .map((t) => t.trim())
      .filter(Boolean);
    const codes = tokens.map((t) => WEEKDAY_CODES[weekdayIndex(t)]);
    return finalizeRecurrence(
      `FREQ=WEEKLY;BYDAY=${codes.join(',')}`,
      weekdayIndex(tokens[0]),
      consume,
      ref,
    );
  }
  if (consume(DAILY_RE)) return finalizeRecurrence('FREQ=DAILY', null, consume, ref);
  if (consume(WEEKLY_RE)) return finalizeRecurrence('FREQ=WEEKLY', null, consume, ref);
  if (consume(MONTHLY_RE)) return finalizeRecurrence('FREQ=MONTHLY', null, consume, ref);
  if (consume(YEARLY_RE)) return finalizeRecurrence('FREQ=YEARLY', null, consume, ref);

  return null;
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

  // --- 1. all-day ("all day", "all-day") ---
  const allDay = consume(ALL_DAY_RE) !== null;

  // --- 2. recurrence ("daily", "every other tuesday", "every mon and wed", ...) ---
  const recurrence = parseRecurrence(consume, ref);
  const recurrenceRule = recurrence?.rule ?? null;

  // --- 3. alerts ("alert 5 min before", "remind me 1 hour before", ...) ---
  const reminderMinutes: number[] = [];
  let alertMatch: RegExpExecArray | null = consume(ALERT_RE);
  while (alertMatch) {
    reminderMinutes.push(alertUnitToMinutes(Number(alertMatch[1]), alertMatch[2]));
    alertMatch = consume(ALERT_RE);
  }

  let hasExplicitTime = false;

  // --- 4a. time range ("12:30-1:30", "2 to 3pm") ---
  let rangeStart: number | null = null;
  let rangeEnd: number | null = null;
  const range = consume(RANGE_RE);
  if (range) {
    hasExplicitTime = true;
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

  // --- 4b. single time ("at 3", "9:15", "7pm", "noon") ---
  let singleMinutes: number | null = null;
  if (rangeStart === null) {
    const at = consume(AT_TIME_RE);
    if (at) {
      hasExplicitTime = true;
      singleMinutes = resolveSingle(Number(at[1]), Number(at[2] ?? '0'), at[3]);
    } else {
      const clock = consume(CLOCK_TIME_RE);
      if (clock) {
        hasExplicitTime = true;
        singleMinutes = resolveSingle(Number(clock[1]), Number(clock[2]), clock[3]);
      } else {
        const mer = consume(MERIDIEM_TIME_RE);
        if (mer) {
          hasExplicitTime = true;
          singleMinutes = resolveSingle(Number(mer[1]), 0, mer[2]);
        } else {
          const word = consume(WORD_TIME_RE);
          if (word) {
            hasExplicitTime = true;
            singleMinutes = word[1].toLowerCase() === 'midnight' ? 0 : 12 * 60;
          }
        }
      }
    }
  }

  // --- 4c. duration ("for 45 min", "for 2 hours", "for a half hour", "for an hour and a half") ---
  let durationMinutes: number | null = null;
  const dur = consume(DURATION_RE);
  if (dur) {
    const n = Number(dur[1]);
    durationMinutes = /^h/i.test(dur[2]) ? Math.round(n * 60) : Math.round(n);
  } else {
    const durWord = consume(DURATION_WORD_RE);
    if (durWord) {
      const phrase = durWord[1].toLowerCase();
      durationMinutes = phrase.startsWith('half') ? 30 : phrase.includes('and a half') ? 90 : 60;
    }
  }

  // --- 4d. date ("today", "tomorrow", "friday", "jul 24", "7/24") ---
  let day: Date | null = null;
  let isTonight = false;
  const dateMatch = findDate(text, ref);
  if (dateMatch) {
    day = dateMatch.date;
    isTonight = dateMatch.isTonight;
    text = `${text.slice(0, dateMatch.index)} ${text.slice(dateMatch.index + dateMatch.length)}`;
    matched = true;
  }

  // Recurring events with a weekday BYDAY but no explicit date anchor to the
  // next occurrence of the first BYDAY weekday.
  if (day === null && recurrence?.anchorWeekday != null) {
    let diff = (recurrence.anchorWeekday - ref.getDay() + 7) % 7;
    if (diff === 0) diff = 7;
    day = startOfDay(addDays(ref, diff));
  }

  // --- 5. attendees ("with Ana", "with ana@acme.com", "with bob@co.com and Ana") ---
  const attendeeEmails: string[] = [];
  const attendeeNames: string[] = [];
  const withMatch = WITH_RE.exec(text);
  if (withMatch) {
    const tokens = withMatch[1]
      .split(/\s*(?:,|and|&|\+)\s*/i)
      .map((t) => t.trim())
      .filter(Boolean);
    const keptTokens: string[] = [];
    for (const tok of tokens) {
      if (tok.includes('@')) {
        attendeeEmails.push(tok);
      } else {
        attendeeNames.push(tok);
        keptTokens.push(tok);
      }
    }
    if (attendeeEmails.length > 0) {
      // Rewrite the "with ..." phrase to drop consumed emails, keeping any
      // remaining names in the title (Fantastical-style behavior).
      const replacement = keptTokens.length > 0 ? `with ${keptTokens.join(' and ')}` : '';
      text = `${text.slice(0, withMatch.index)} ${replacement} ${text.slice(withMatch.index + withMatch[0].length)}`;
      matched = true;
    }
  }

  // --- 6. location ("at Blue Bottle", "in Conference Room B", "@ Lake Tahoe") ---
  let location: string | null = null;
  const locPeek = LOCATION_RE.exec(text);
  if (locPeek) {
    const captured = locPeek[1].trim();
    if (captured && !/^\d+$/.test(captured)) {
      location = captured;
      consume(LOCATION_RE);
    }
  }

  // --- assemble ---
  const base = day ?? startOfDay(ref);
  let start: Date;
  let end: Date;

  if (allDay) {
    start = startOfDay(base);
    end = startOfDay(addDays(base, 1));
  } else if (rangeStart !== null && rangeEnd !== null) {
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
    allDay,
    matched,
    location,
    recurrenceRule,
    reminderMinutes,
    attendeeEmails,
    attendeeNames,
    durationMinutes,
    hasExplicitTime,
  };
}
