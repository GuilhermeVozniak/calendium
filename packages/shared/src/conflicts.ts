import type { Attendee, Calendar, Event } from './types';

/**
 * Cross-account blocking core shared by web, desktop, and mobile. Callers
 * pass the *unfiltered* event fetch (all calendars, all accounts, hidden
 * calendars included) so a hidden or other-account calendar can still block
 * time on the primary calendar — mirrors the backend's
 * CalendarService.Availability rules (backend/internal/service/calendar.go).
 *
 * Named ConflictInterval (not BusyInterval) to avoid colliding with the
 * backend-mirroring domain.BusyInterval (start/end as ISO strings, from the
 * guest free/busy API) exported from ./types — this one carries event
 * provenance (eventId/calendarId/accountId/title) and Date fields for local
 * conflict-detection math.
 */
export interface ConflictInterval {
  start: Date;
  end: Date;
  eventId: string;
  calendarId: string;
  accountId: string;
  title: string;
}

/** Half-open overlap test: [aS,aE) ∩ [bS,bE) ≠ ∅. */
export function overlaps(aS: Date, aE: Date, bS: Date, bE: Date): boolean {
  return aS < bE && bS < aE;
}

function declinedByUser(ev: Event, ownEmails: ReadonlySet<string>): boolean {
  for (const a of ev.attendees as Attendee[]) {
    if (ownEmails.has(a.email.toLowerCase())) {
      return a.response === 'declined';
    }
  }
  return false;
}

/**
 * Busy = non-cancelled, non-all-day, not declined by the user (ownEmails =
 * every connected account address). Mirrors the backend Availability rules.
 */
export function toBusyIntervals(
  events: Event[],
  calendars: Calendar[],
  ownEmails: string[],
  opts: { ignoreEventId?: string } = {}
): ConflictInterval[] {
  const calendarById = new Map(calendars.map((c) => [c.id, c]));
  const ownEmailSet = new Set(ownEmails.map((e) => e.toLowerCase()));
  const out: ConflictInterval[] = [];
  for (const ev of events) {
    if (opts.ignoreEventId && ev.id === opts.ignoreEventId) continue;
    if (ev.status === 'cancelled' || ev.allDay) continue;
    if (declinedByUser(ev, ownEmailSet)) continue;
    out.push({
      start: new Date(ev.start),
      end: new Date(ev.end),
      eventId: ev.id,
      calendarId: ev.calendarId,
      accountId: calendarById.get(ev.calendarId)?.accountId ?? '',
      title: ev.title,
    });
  }
  return out;
}

/** Sorted-sweep merge of overlapping/adjacent intervals. */
export function mergeBusy(intervals: ConflictInterval[]): Array<{ start: Date; end: Date }> {
  if (intervals.length === 0) return [];
  const sorted = [...intervals].sort((a, b) => a.start.getTime() - b.start.getTime());
  const out: Array<{ start: Date; end: Date }> = [];
  // Non-null: `sorted` is non-empty (the length===0 case returns above), and
  // `i` stays within [1, sorted.length) in the loop below.
  let current = { start: sorted[0]!.start, end: sorted[0]!.end };
  for (let i = 1; i < sorted.length; i++) {
    const next = sorted[i]!;
    if (next.start.getTime() <= current.end.getTime()) {
      if (next.end.getTime() > current.end.getTime()) current = { ...current, end: next.end };
    } else {
      out.push(current);
      current = { start: next.start, end: next.end };
    }
  }
  out.push(current);
  return out;
}

/** Existing events overlapping a candidate slot → double-booking warning list. */
export function findConflicts(start: Date, end: Date, busy: ConflictInterval[]): ConflictInterval[] {
  return busy.filter((b) => overlaps(start, end, b.start, b.end));
}

/**
 * Gaps of >= durationMinutes between merged busy intervals inside [from, to).
 * Used for quick-add slot suggestions; caller pre-clamps to working hours.
 */
export function suggestFreeSlots(
  from: Date,
  to: Date,
  durationMinutes: number,
  busy: ConflictInterval[]
): Array<{ start: Date; end: Date }> {
  const merged = mergeBusy(busy.filter((b) => overlaps(b.start, b.end, from, to)));
  const out: Array<{ start: Date; end: Date }> = [];
  let cursor = from;
  for (const b of merged) {
    if (b.start.getTime() - cursor.getTime() >= durationMinutes * 60_000)
      out.push({ start: cursor, end: b.start });
    if (b.end > cursor) cursor = b.end;
  }
  if (to.getTime() - cursor.getTime() >= durationMinutes * 60_000)
    out.push({ start: cursor, end: to });
  return out;
}
