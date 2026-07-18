import { describe, expect, it } from 'vitest';

import { detectConference, isJoinable } from './conferencing';

describe('detectConference', () => {
  it('matches a zoom /j/ URL with a password query param', () => {
    const result = detectConference({
      conferencing: null,
      location: 'https://zoom.us/j/1234567890?pwd=abc123',
      description: null,
    });
    expect(result).toEqual({
      provider: 'zoom',
      url: 'https://zoom.us/j/1234567890?pwd=abc123',
    });
  });

  it('matches a zoom personal room /my/ URL', () => {
    const result = detectConference({
      conferencing: null,
      location: 'Join at https://zoom.us/my/myroomname for the call',
      description: null,
    });
    expect(result).toEqual({ provider: 'zoom', url: 'https://zoom.us/my/myroomname' });
  });

  it('matches a zoom URL on a vanity subdomain', () => {
    const result = detectConference({
      conferencing: null,
      location: 'https://acme.zoom.us/j/999',
      description: null,
    });
    expect(result).toEqual({ provider: 'zoom', url: 'https://acme.zoom.us/j/999' });
  });

  it('matches a Google Meet code without a query string', () => {
    const result = detectConference({
      conferencing: null,
      location: 'https://meet.google.com/abc-defg-hij',
      description: null,
    });
    expect(result).toEqual({ provider: 'meet', url: 'https://meet.google.com/abc-defg-hij' });
  });

  it('matches a Google Meet code with a trailing query string', () => {
    const result = detectConference({
      conferencing: null,
      location: 'https://meet.google.com/abc-defg-hij?authuser=0',
      description: null,
    });
    expect(result).toEqual({
      provider: 'meet',
      url: 'https://meet.google.com/abc-defg-hij?authuser=0',
    });
  });

  it('matches a percent-encoded Teams l/meetup-join URL', () => {
    const url =
      'https://teams.microsoft.com/l/meetup-join/19%3ameeting_abc%40thread.v2/0?context=%7b%22Tid%22%3a%22xyz%22%7d';
    const result = detectConference({ conferencing: null, location: url, description: null });
    expect(result).toEqual({ provider: 'teams', url });
  });

  it('matches a teams.live.com /meet/ URL', () => {
    const result = detectConference({
      conferencing: null,
      location: 'https://teams.live.com/meet/12345',
      description: null,
    });
    expect(result).toEqual({ provider: 'teams', url: 'https://teams.live.com/meet/12345' });
  });

  it('matches a webex /meet/user URL', () => {
    const result = detectConference({
      conferencing: null,
      location: 'https://acme.webex.com/meet/jdoe',
      description: null,
    });
    expect(result).toEqual({ provider: 'webex', url: 'https://acme.webex.com/meet/jdoe' });
  });

  it('matches a webex legacy /j.php URL', () => {
    const result = detectConference({
      conferencing: null,
      location: 'https://acme.webex.com/acme/j.php?MTID=abc123',
      description: null,
    });
    expect(result).toEqual({
      provider: 'webex',
      url: 'https://acme.webex.com/acme/j.php?MTID=abc123',
    });
  });

  it('prefers the structured conferencing field over location/description', () => {
    const result = detectConference({
      conferencing: { provider: 'zoom', url: 'https://zoom.us/j/structured' },
      location: 'https://meet.google.com/abc-defg-hij',
      description: 'https://acme.webex.com/meet/jdoe',
    });
    expect(result).toEqual({ provider: 'zoom', url: 'https://zoom.us/j/structured' });
  });

  it('prefers location over description when both contain a link', () => {
    const result = detectConference({
      conferencing: null,
      location: 'https://meet.google.com/abc-defg-hij',
      description: 'https://acme.webex.com/meet/jdoe',
    });
    expect(result).toEqual({ provider: 'meet', url: 'https://meet.google.com/abc-defg-hij' });
  });

  it('falls back to description when location has no conference link', () => {
    const result = detectConference({
      conferencing: null,
      location: 'Conference room 4B',
      description: 'Dial in at https://acme.webex.com/meet/jdoe',
    });
    expect(result).toEqual({ provider: 'webex', url: 'https://acme.webex.com/meet/jdoe' });
  });

  it('returns null when location and description are plain text', () => {
    const result = detectConference({
      conferencing: null,
      location: 'Conference room 4B',
      description: 'Bring your laptop.',
    });
    expect(result).toBeNull();
  });

  it('returns null when conferencing, location, and description are all null', () => {
    const result = detectConference({ conferencing: null, location: null, description: null });
    expect(result).toBeNull();
  });
});

describe('isJoinable', () => {
  const start = new Date('2026-08-01T09:00:00Z');
  const end = new Date('2026-08-01T09:30:00Z');

  it('is joinable exactly leadMinutes before start (inclusive)', () => {
    const now = new Date('2026-08-01T08:55:00Z');
    expect(isJoinable(now, start, end)).toBe(true);
  });

  it('is not joinable one millisecond before the lead window opens', () => {
    const now = new Date('2026-08-01T08:54:59.999Z');
    expect(isJoinable(now, start, end)).toBe(false);
  });

  it('is joinable during the event', () => {
    const now = new Date('2026-08-01T09:10:00Z');
    expect(isJoinable(now, start, end)).toBe(true);
  });

  it('is not joinable at or after the end time (exclusive)', () => {
    expect(isJoinable(end, start, end)).toBe(false);
    expect(isJoinable(new Date('2026-08-01T09:31:00Z'), start, end)).toBe(false);
  });

  it('respects a custom leadMinutes', () => {
    const now = new Date('2026-08-01T08:50:00Z');
    expect(isJoinable(now, start, end, 10)).toBe(true);
    expect(isJoinable(now, start, end, 5)).toBe(false);
  });
});
