import type { Conferencing } from '@calendium/shared';
import { getJoinInfo } from './calendar-join';

interface Fields {
  conferencing: Conferencing | null;
  location: string | null;
  description: string | null;
  start: string;
  end: string;
}

function baseEvent(overrides: Partial<Fields> = {}): Fields {
  return {
    conferencing: null,
    location: null,
    description: null,
    start: new Date('2026-08-01T09:00:00Z').toISOString(),
    end: new Date('2026-08-01T09:30:00Z').toISOString(),
    ...overrides,
  };
}

describe('getJoinInfo', () => {
  it('returns null when no conference link is detected', () => {
    expect(getJoinInfo(baseEvent())).toBeNull();
  });

  it('returns null before the joinable lead window opens', () => {
    const event = baseEvent({ location: 'https://meet.google.com/abc-defg-hij' });
    expect(getJoinInfo(event, new Date('2026-08-01T08:54:59.999Z'))).toBeNull();
  });

  it('returns join info exactly at the lead window (5 min before start)', () => {
    const event = baseEvent({ location: 'https://meet.google.com/abc-defg-hij' });
    expect(getJoinInfo(event, new Date('2026-08-01T08:55:00Z'))).toEqual({
      url: 'https://meet.google.com/abc-defg-hij',
      label: 'Join Meet',
    });
  });

  it('returns join info during the event', () => {
    const event = baseEvent({ location: 'https://zoom.us/j/1234567890' });
    expect(getJoinInfo(event, new Date('2026-08-01T09:10:00Z'))).toEqual({
      url: 'https://zoom.us/j/1234567890',
      label: 'Join Zoom',
    });
  });

  it('returns null at/after the event end', () => {
    const event = baseEvent({ location: 'https://teams.live.com/meet/12345' });
    expect(getJoinInfo(event, new Date('2026-08-01T09:30:00Z'))).toBeNull();
    expect(getJoinInfo(event, new Date('2026-08-01T09:45:00Z'))).toBeNull();
  });

  it('labels a webex link', () => {
    const event = baseEvent({ location: 'https://acme.webex.com/meet/jdoe' });
    expect(getJoinInfo(event, new Date('2026-08-01T09:10:00Z'))?.label).toBe('Join Webex');
  });

  it('prefers the structured conferencing field over location', () => {
    const event = baseEvent({
      conferencing: { provider: 'teams', url: 'https://teams.microsoft.com/l/meetup-join/x' },
      location: 'https://meet.google.com/abc-defg-hij',
    });
    expect(getJoinInfo(event, new Date('2026-08-01T09:10:00Z'))).toEqual({
      url: 'https://teams.microsoft.com/l/meetup-join/x',
      label: 'Join Teams',
    });
  });

  it('defaults `now` to the current time when omitted', () => {
    jest.useFakeTimers();
    jest.setSystemTime(new Date('2026-08-01T09:10:00Z'));
    const event = baseEvent({ location: 'https://meet.google.com/abc-defg-hij' });
    expect(getJoinInfo(event)).toEqual({
      url: 'https://meet.google.com/abc-defg-hij',
      label: 'Join Meet',
    });
    jest.useRealTimers();
  });
});
