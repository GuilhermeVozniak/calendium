import type { Event } from '@calendium/shared';
import { beforeEach, describe, expect, it } from 'vitest';

import {
  AUTO_JOIN_STORAGE_KEY,
  loadAutoJoinSettings,
  saveAutoJoinSettings,
  selectTrayEvents,
} from './tray';

const NOW = new Date('2026-07-19T10:00:00Z');

function minutes(n: number): Date {
  return new Date(NOW.getTime() + n * 60_000);
}

function makeEvent(overrides: Partial<Event> = {}): Event {
  return {
    id: 'ev_1',
    calendarId: 'cal_1',
    title: 'Standup',
    description: null,
    location: null,
    start: minutes(30).toISOString(),
    end: minutes(60).toISOString(),
    allDay: false,
    recurrenceRule: null,
    attendees: [],
    conferencing: null,
    status: 'confirmed',
    visibility: 'default',
    reminderMinutes: [],
    ...overrides,
  };
}

describe('selectTrayEvents', () => {
  it('sorts by start time and maps to the tray payload', () => {
    const events = [
      makeEvent({ id: 'b', title: 'Later', start: minutes(120).toISOString(), end: minutes(150).toISOString() }),
      makeEvent({ id: 'a', title: 'Sooner', start: minutes(15).toISOString(), end: minutes(45).toISOString() }),
    ];
    const out = selectTrayEvents(events, NOW);
    expect(out.map((e) => e.id)).toEqual(['a', 'b']);
    expect(out[0]).toEqual({
      id: 'a',
      title: 'Sooner',
      startAt: minutes(15).toISOString(),
      joinUrl: '',
    });
  });

  it('keeps in-progress events (started, not ended) for the "now" countdown', () => {
    const events = [
      makeEvent({ id: 'running', start: minutes(-10).toISOString(), end: minutes(20).toISOString() }),
    ];
    expect(selectTrayEvents(events, NOW).map((e) => e.id)).toEqual(['running']);
  });

  it('drops ended events, events beyond 12h, all-day events, and cancelled events', () => {
    const events = [
      makeEvent({ id: 'ended', start: minutes(-60).toISOString(), end: minutes(-5).toISOString() }),
      makeEvent({ id: 'far', start: minutes(13 * 60).toISOString(), end: minutes(14 * 60).toISOString() }),
      makeEvent({ id: 'allday', allDay: true }),
      makeEvent({ id: 'cancelled', status: 'cancelled' }),
      makeEvent({ id: 'ok' }),
    ];
    expect(selectTrayEvents(events, NOW).map((e) => e.id)).toEqual(['ok']);
  });

  it('caps the feed at 5 events', () => {
    const events = Array.from({ length: 8 }, (_, i) =>
      makeEvent({
        id: `ev_${i}`,
        start: minutes(10 + i).toISOString(),
        end: minutes(40 + i).toISOString(),
      })
    );
    expect(selectTrayEvents(events, NOW)).toHaveLength(5);
  });

  it('extracts the join URL from the structured conferencing field', () => {
    const events = [
      makeEvent({ conferencing: { provider: 'meet', url: 'https://meet.google.com/abc-defg-hij' } }),
    ];
    expect(selectTrayEvents(events, NOW)[0]?.joinUrl).toBe('https://meet.google.com/abc-defg-hij');
  });

  it('sniffs a join URL out of the location via shared detectConference', () => {
    const events = [makeEvent({ location: 'Join: https://zoom.us/j/123456?pwd=x' })];
    expect(selectTrayEvents(events, NOW)[0]?.joinUrl).toBe('https://zoom.us/j/123456?pwd=x');
  });

  it('leaves joinUrl empty when the event has no conferencing anywhere', () => {
    const events = [makeEvent({ location: 'Room 4B', description: 'Bring notes' })];
    expect(selectTrayEvents(events, NOW)[0]?.joinUrl).toBe('');
  });
});

describe('auto-join settings persistence', () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it('defaults to disabled at lead 0 when nothing is stored', () => {
    expect(loadAutoJoinSettings()).toEqual({ enabled: false, leadSeconds: 0 });
  });

  it('round-trips through save/load', () => {
    saveAutoJoinSettings({ enabled: true, leadSeconds: 30 });
    expect(loadAutoJoinSettings()).toEqual({ enabled: true, leadSeconds: 30 });
  });

  it('falls back to the default on malformed JSON', () => {
    localStorage.setItem(AUTO_JOIN_STORAGE_KEY, '{nope');
    expect(loadAutoJoinSettings()).toEqual({ enabled: false, leadSeconds: 0 });
  });

  it('falls back to the default on an out-of-range lead', () => {
    localStorage.setItem(AUTO_JOIN_STORAGE_KEY, JSON.stringify({ enabled: true, leadSeconds: 45 }));
    expect(loadAutoJoinSettings()).toEqual({ enabled: false, leadSeconds: 0 });
  });
});
