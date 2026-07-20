import type { EventInput } from '@calendium/shared';
import { addMinutes } from 'date-fns';

import { GRID_HOUR_HEIGHT, dropTimeForDay } from '@/lib/task-drag';

/**
 * Drag-and-drop contract for dragging an email thread onto the calendar
 * (M2.8 Task 18 — email-to-event). Mirrors lib/task-drag.ts (Task 3) and
 * reuses its pixel→time math; only the payload differs — a small JSON
 * document instead of a bare id, so a drop target can prefill an event
 * (title/attendees/description) without another fetch.
 *
 * Contract: a thread row calls `setThreadDragData(e.dataTransfer, thread)`;
 * a grid day column decodes with `decodeThreadDrag`, computes the drop time
 * with `threadDropTime` (task-drag's dropTimeForDay at a finer 15-minute
 * snap), and opens EventDialog with `eventPrefillFromThread(...)` as its
 * `prefill` prop. Dropping on an all-day header passes `allDay: true`.
 */

/** Custom MIME type carrying the dragged thread's payload. */
export const THREAD_DRAG_TYPE = 'application/x-calendium-thread';

/** Snap for thread drops — finer than the 30-minute task blocks (per the Task 18 brief). */
export const THREAD_DROP_SNAP_MINUTES = 15;

/** Default duration of an event created from a thread drop. */
export const THREAD_EVENT_MINUTES = 30;

export interface ThreadDragParticipant {
  email: string;
  name?: string;
}

export interface ThreadDragPayload {
  threadId: string;
  subject: string;
  participants: ThreadDragParticipant[];
}

/** Loose input shape so a shared `Thread` (name: string | null) can be passed as-is. */
interface ThreadDragInput {
  threadId: string;
  subject: string;
  participants: { email: string; name?: string | null }[];
}

/** Serializes exactly the contract fields — extra keys and null names are dropped. */
export function encodeThreadDrag(t: ThreadDragInput): string {
  const payload: ThreadDragPayload = {
    threadId: t.threadId,
    subject: t.subject,
    participants: t.participants.map((p) =>
      p.name ? { email: p.email, name: p.name } : { email: p.email }
    ),
  };
  return JSON.stringify(payload);
}

export function setThreadDragData(dt: DataTransfer, t: ThreadDragInput): void {
  dt.setData(THREAD_DRAG_TYPE, encodeThreadDrag(t));
  // A drop creates a new event; the thread itself never moves.
  dt.effectAllowed = 'copy';
}

export function hasThreadDrag(dt: DataTransfer | null): boolean {
  if (!dt) return false;
  return Array.from(dt.types ?? []).includes(THREAD_DRAG_TYPE);
}

/** Parses and validates the payload; any malformed/foreign data → null. */
export function decodeThreadDrag(dt: DataTransfer | null): ThreadDragPayload | null {
  if (!dt) return null;
  const raw = dt.getData(THREAD_DRAG_TYPE);
  if (!raw) return null;
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    return null;
  }
  if (typeof parsed !== 'object' || parsed === null) return null;
  const p = parsed as Record<string, unknown>;
  if (typeof p.threadId !== 'string' || !p.threadId) return null;
  if (typeof p.subject !== 'string') return null;
  if (!Array.isArray(p.participants)) return null;
  const participants: ThreadDragParticipant[] = [];
  for (const item of p.participants) {
    if (typeof item !== 'object' || item === null) return null;
    const a = item as Record<string, unknown>;
    if (typeof a.email !== 'string' || !a.email) return null;
    participants.push(
      typeof a.name === 'string' && a.name ? { email: a.email, name: a.name } : { email: a.email }
    );
  }
  return { threadId: p.threadId, subject: p.subject, participants };
}

/** Drop time over a day column — task-drag's grid math, snapped to 15 minutes. */
export function threadDropTime(
  day: Date,
  clientY: number,
  rectTop: number,
  hourHeight: number = GRID_HOUR_HEIGHT
): Date {
  return dropTimeForDay(day, clientY, rectTop, hourHeight, THREAD_DROP_SNAP_MINUTES);
}

/** `mailto:`-style reference line pointing back at the thread's participants. */
export function threadReferenceLine(t: ThreadDragPayload): string {
  const subject = t.subject.trim() || '(No subject)';
  const emails = t.participants.map((p) => p.email).join(',');
  return emails
    ? `From email thread “${subject}” — mailto:${emails}`
    : `From email thread “${subject}”`;
}

/**
 * The EventDialog `prefill` for a thread dropped at `start`: title from the
 * subject, attendees from the participants (deduped case-insensitively),
 * and a mailto-style thread reference line as the description.
 */
export function eventPrefillFromThread(
  t: ThreadDragPayload,
  start: Date,
  allDay = false
): Partial<EventInput> & { sourceThreadId: string } {
  const seen = new Set<string>();
  const attendeeEmails = t.participants
    .map((p) => p.email)
    .filter((email) => {
      const key = email.toLowerCase();
      if (seen.has(key)) return false;
      seen.add(key);
      return true;
    });
  return {
    title: t.subject.trim() || '(No subject)',
    description: threadReferenceLine(t),
    start: start.toISOString(),
    end: addMinutes(start, THREAD_EVENT_MINUTES).toISOString(),
    allDay,
    ...(attendeeEmails.length > 0 ? { attendeeEmails } : {}),
    sourceThreadId: t.threadId,
  };
}
