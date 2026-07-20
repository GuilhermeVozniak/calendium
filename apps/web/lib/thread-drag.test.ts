import { describe, expect, it, vi } from 'vitest';

import {
  THREAD_DRAG_TYPE,
  THREAD_EVENT_MINUTES,
  decodeThreadDrag,
  encodeThreadDrag,
  eventPrefillFromThread,
  hasThreadDrag,
  setThreadDragData,
  threadDropTime,
  type ThreadDragPayload,
} from '@/lib/thread-drag';

/** Minimal DataTransfer stub — jsdom has no real constructor. */
function makeDataTransfer(data: Record<string, string> = {}) {
  const store: Record<string, string> = { ...data };
  return {
    setData: vi.fn((type: string, value: string) => {
      store[type] = value;
    }),
    getData: vi.fn((type: string) => store[type] ?? ''),
    get types() {
      return Object.keys(store);
    },
    effectAllowed: '',
    dropEffect: '',
  } as unknown as DataTransfer;
}

const PAYLOAD: ThreadDragPayload = {
  threadId: 'thr-1',
  subject: 'Renewal terms for FY27',
  participants: [{ email: 'ana@acme.com', name: 'Ana' }, { email: 'bob@acme.com' }],
};

describe('thread drag payload', () => {
  it('round-trips through encode + DataTransfer', () => {
    const dt = makeDataTransfer();
    setThreadDragData(dt, PAYLOAD);
    expect(dt.setData).toHaveBeenCalledWith(THREAD_DRAG_TYPE, encodeThreadDrag(PAYLOAD));
    expect(hasThreadDrag(dt)).toBe(true);
    expect(decodeThreadDrag(dt)).toEqual(PAYLOAD);
  });

  it('encodes only the contract fields, dropping null names and extras', () => {
    const dirty = {
      threadId: 't1',
      subject: 's',
      participants: [{ email: 'a@b.co', name: null, response: 'yes' }],
      snippet: 'not part of the payload',
    };
    expect(JSON.parse(encodeThreadDrag(dirty as never))).toEqual({
      threadId: 't1',
      subject: 's',
      participants: [{ email: 'a@b.co' }],
    });
  });

  it('returns null / false for missing or malformed payloads', () => {
    expect(decodeThreadDrag(null)).toBeNull();
    expect(decodeThreadDrag(makeDataTransfer())).toBeNull();
    expect(decodeThreadDrag(makeDataTransfer({ [THREAD_DRAG_TYPE]: 'not json' }))).toBeNull();
    expect(
      decodeThreadDrag(makeDataTransfer({ [THREAD_DRAG_TYPE]: JSON.stringify({ subject: 'x' }) }))
    ).toBeNull();
    expect(
      decodeThreadDrag(
        makeDataTransfer({
          [THREAD_DRAG_TYPE]: JSON.stringify({
            threadId: 't',
            subject: 's',
            participants: [{ name: 'missing email' }],
          }),
        })
      )
    ).toBeNull();
    expect(hasThreadDrag(null)).toBe(false);
    expect(hasThreadDrag(makeDataTransfer({ 'text/plain': 'x' }))).toBe(false);
  });
});

describe('threadDropTime (shared grid math from task-drag, 15-minute snap)', () => {
  const day = new Date(2026, 6, 23); // Thu Jul 23 2026, local midnight

  it('snaps the pointer down to the previous 15-minute slot', () => {
    // 500px at 48px/hour = 625 clock minutes (10:25) → snaps to 10:15.
    const t = threadDropTime(day, 500, 0);
    expect(t.getHours()).toBe(10);
    expect(t.getMinutes()).toBe(15);
  });

  it('subtracts the column top before converting to minutes', () => {
    // 48px into the column = one hour into the day.
    const t = threadDropTime(day, 148, 100);
    expect(t.getHours()).toBe(1);
    expect(t.getMinutes()).toBe(0);
  });

  it('clamps to the top and bottom of the day', () => {
    const early = threadDropTime(day, -50, 0);
    expect(early.getHours()).toBe(0);
    expect(early.getMinutes()).toBe(0);
    const late = threadDropTime(day, 48 * 25, 0);
    expect(late.getHours()).toBe(23);
    expect(late.getMinutes()).toBe(45);
  });
});

describe('eventPrefillFromThread', () => {
  const start = new Date(2026, 6, 23, 14, 0, 0, 0);

  it('prefills title, attendees, description and a 30-minute slot', () => {
    const p = eventPrefillFromThread(PAYLOAD, start);
    expect(p.title).toBe('Renewal terms for FY27');
    expect(p.attendeeEmails).toEqual(['ana@acme.com', 'bob@acme.com']);
    expect(p.start).toBe(start.toISOString());
    expect(new Date(p.end!).getTime() - start.getTime()).toBe(THREAD_EVENT_MINUTES * 60_000);
    expect(p.allDay).toBe(false);
    expect(p.sourceThreadId).toBe('thr-1');
    // mailto-style thread reference line in the description.
    expect(p.description).toContain('mailto:ana@acme.com,bob@acme.com');
    expect(p.description).toContain('Renewal terms for FY27');
  });

  it('dedupes participant emails case-insensitively', () => {
    const p = eventPrefillFromThread(
      { ...PAYLOAD, participants: [{ email: 'Ana@acme.com' }, { email: 'ana@acme.com' }] },
      start
    );
    expect(p.attendeeEmails).toEqual(['Ana@acme.com']);
  });

  it('marks an all-day drop as all-day', () => {
    const p = eventPrefillFromThread(PAYLOAD, start, true);
    expect(p.allDay).toBe(true);
    expect(p.start).toBe(start.toISOString());
  });

  it('falls back to "(No subject)" for a blank subject', () => {
    const p = eventPrefillFromThread({ ...PAYLOAD, subject: '   ' }, start);
    expect(p.title).toBe('(No subject)');
    expect(p.description).toContain('(No subject)');
  });
});
