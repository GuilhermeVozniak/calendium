import { afterEach, beforeEach, describe, expect, it } from 'vitest';

import {
  PINNED_KEY,
  getPinnedTimeZones,
  hourLabelInZone,
  setPinnedTimeZones,
  tzOffsetMinutes,
  zoneCaption,
} from '@/lib/timezones';

describe('tzOffsetMinutes', () => {
  it('returns -240 for America/New_York in summer (EDT, DST active)', () => {
    expect(tzOffsetMinutes('America/New_York', new Date('2026-07-01T12:00:00Z'))).toBe(-240);
  });

  it('returns -300 for America/New_York in winter (EST, DST inactive)', () => {
    expect(tzOffsetMinutes('America/New_York', new Date('2026-01-01T12:00:00Z'))).toBe(-300);
  });

  it('returns +330 for a half-hour-offset zone (Asia/Kolkata)', () => {
    expect(tzOffsetMinutes('Asia/Kolkata', new Date('2026-07-01T12:00:00Z'))).toBe(330);
  });

  it('returns 0 for UTC', () => {
    expect(tzOffsetMinutes('UTC', new Date('2026-07-01T12:00:00Z'))).toBe(0);
  });
});

describe('hourLabelInZone', () => {
  let originalTz: string | undefined;

  beforeEach(() => {
    originalTz = process.env.TZ;
  });

  afterEach(() => {
    process.env.TZ = originalTz;
  });

  it('shifts a day forward across the dateline (LA evening -> Auckland next day)', () => {
    process.env.TZ = 'America/Los_Angeles';
    // Jan 15, 10 PM in Los Angeles (primary grid day/hour) lands on Jan 16,
    // 7 PM in Auckland (NZDT, UTC+13 in January) -> the gutter must mark it
    // as "next day" or the label would look like it's earlier than 10 PM.
    const day = new Date(2026, 0, 15);
    const result = hourLabelInZone(day, 22, 'Pacific/Auckland');
    expect(result.label).toBe('7 PM');
    expect(result.dayShift).toBe(1);
  });

  it('shifts a day backward across the dateline (Auckland early morning -> LA previous day)', () => {
    process.env.TZ = 'Pacific/Auckland';
    // Jan 15, 2 AM in Auckland lands on Jan 14, 5 AM in Los Angeles (PST,
    // UTC-8 in January) -> the gutter must mark it as "previous day".
    const day = new Date(2026, 0, 15);
    const result = hourLabelInZone(day, 2, 'America/Los_Angeles');
    expect(result.label).toBe('5 AM');
    expect(result.dayShift).toBe(-1);
  });

  it('stays on the same day when the shift does not cross midnight', () => {
    process.env.TZ = 'UTC';
    const day = new Date(2026, 0, 15);
    const result = hourLabelInZone(day, 12, 'Asia/Kolkata');
    expect(result.label).toBe('5:30 PM');
    expect(result.dayShift).toBe(0);
  });

  it('formats midnight as 12 AM', () => {
    process.env.TZ = 'UTC';
    const day = new Date(2026, 0, 15);
    const result = hourLabelInZone(day, 0, 'UTC');
    expect(result.label).toBe('12 AM');
    expect(result.dayShift).toBe(0);
  });

  it('formats noon as 12 PM', () => {
    process.env.TZ = 'UTC';
    const day = new Date(2026, 0, 15);
    const result = hourLabelInZone(day, 12, 'UTC');
    expect(result.label).toBe('12 PM');
    expect(result.dayShift).toBe(0);
  });
});

describe('zoneCaption', () => {
  it('produces a GMT-offset label with the correct sign for a zone behind UTC', () => {
    const { gmt } = zoneCaption('America/New_York', new Date('2026-07-01T12:00:00Z'));
    expect(gmt).toBe('GMT-4');
  });

  it('produces a GMT-offset label with minutes for a half-hour zone', () => {
    const { gmt } = zoneCaption('Asia/Kolkata', new Date('2026-07-01T12:00:00Z'));
    expect(gmt).toBe('GMT+5:30');
  });

  it('derives a readable city label from the IANA id', () => {
    const { city } = zoneCaption('Asia/Kolkata', new Date('2026-07-01T12:00:00Z'));
    expect(city).toBe('Kolkata');
  });

  it('replaces underscores in multi-word city names not in the shorthand map', () => {
    const { city } = zoneCaption(
      'America/Argentina/Buenos_Aires',
      new Date('2026-07-01T12:00:00Z')
    );
    expect(city).toBe('Buenos Aires');
  });
});

describe('getPinnedTimeZones / setPinnedTimeZones', () => {
  beforeEach(() => {
    window.localStorage.clear();
  });

  it('returns an empty array when nothing is stored', () => {
    expect(getPinnedTimeZones()).toEqual([]);
  });

  it('round-trips a set of valid zones through localStorage', () => {
    setPinnedTimeZones(['America/New_York', 'Europe/London']);
    expect(getPinnedTimeZones()).toEqual(['America/New_York', 'Europe/London']);
    expect(window.localStorage.getItem(PINNED_KEY)).toBe(
      JSON.stringify(['America/New_York', 'Europe/London'])
    );
  });

  it('caps the pinned set at 3 zones', () => {
    setPinnedTimeZones(['America/New_York', 'Europe/London', 'Asia/Kolkata', 'Asia/Tokyo']);
    expect(getPinnedTimeZones()).toEqual(['America/New_York', 'Europe/London', 'Asia/Kolkata']);
  });

  it('dedupes repeated zones before capping', () => {
    setPinnedTimeZones(['America/New_York', 'America/New_York', 'Europe/London']);
    expect(getPinnedTimeZones()).toEqual(['America/New_York', 'Europe/London']);
  });

  it('drops invalid IANA zone ids', () => {
    setPinnedTimeZones(['Not/AZone', 'Europe/London', '']);
    expect(getPinnedTimeZones()).toEqual(['Europe/London']);
  });

  it('ignores malformed JSON already in storage', () => {
    window.localStorage.setItem(PINNED_KEY, 'not json');
    expect(getPinnedTimeZones()).toEqual([]);
  });

  it('ignores a non-array value already in storage', () => {
    window.localStorage.setItem(PINNED_KEY, JSON.stringify({ foo: 'bar' }));
    expect(getPinnedTimeZones()).toEqual([]);
  });
});
