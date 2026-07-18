import { afterEach, beforeEach, describe, expect, it } from 'vitest';

import {
  browserTimeZone,
  formatInTZ,
  listTimeZones,
  tzAbbrev,
  tzOffsetLabel,
} from './timezone';

// A fixed instant in the northern-hemisphere summer, when America/New_York
// observes EDT (UTC-4). 2026-01-15 is used for the winter/EST (UTC-5) half of
// the DST-boundary assertions below.
const SUMMER = '2026-07-15T14:00:00.000Z'; // 10:00 EDT
const WINTER = '2026-01-15T14:00:00.000Z'; // 09:00 EST

describe('formatInTZ', () => {
  it('formats a time in the given zone', () => {
    expect(formatInTZ(SUMMER, 'America/New_York', 'time')).toBe('10:00 AM');
  });

  it('formats a weekday + date in the given zone', () => {
    expect(formatInTZ(SUMMER, 'America/New_York', 'weekday-date')).toBe('Wednesday, July 15');
  });

  it('formats a combined weekday, date, and time in the given zone', () => {
    expect(formatInTZ(SUMMER, 'America/New_York', 'datetime')).toBe('Wed, Jul 15, 10:00 AM');
  });

  it('is DST-aware across the summer/winter boundary for the same zone', () => {
    // Same wall-clock instant formatting logic, but the underlying UTC offset
    // differs by an hour depending on the time of year — Intl (not fixed
    // arithmetic) must resolve this correctly.
    expect(formatInTZ(SUMMER, 'America/New_York', 'time')).toBe('10:00 AM');
    expect(formatInTZ(WINTER, 'America/New_York', 'time')).toBe('9:00 AM');
  });

  it('formats the same instant differently across zones', () => {
    expect(formatInTZ(SUMMER, 'UTC', 'time')).toBe('2:00 PM');
    expect(formatInTZ(SUMMER, 'America/Los_Angeles', 'time')).toBe('7:00 AM');
  });
});

describe('tzOffsetLabel', () => {
  it('returns a GMT offset label that shifts across DST', () => {
    expect(tzOffsetLabel('America/New_York', new Date(SUMMER))).toBe('GMT-4');
    expect(tzOffsetLabel('America/New_York', new Date(WINTER))).toBe('GMT-5');
  });

  it('returns GMT+0 for UTC', () => {
    expect(tzOffsetLabel('UTC', new Date(SUMMER))).toBe('GMT+0');
  });

  it('defaults `at` to now when omitted', () => {
    expect(() => tzOffsetLabel('UTC')).not.toThrow();
  });
});

describe('tzAbbrev', () => {
  it('returns the short abbreviation, DST-aware', () => {
    expect(tzAbbrev('America/New_York', new Date(SUMMER))).toBe('EDT');
    expect(tzAbbrev('America/New_York', new Date(WINTER))).toBe('EST');
  });

  it('falls back to a GMT offset string for zones without a short abbreviation', () => {
    expect(tzAbbrev('Asia/Kolkata', new Date(SUMMER))).toBe('GMT+5:30');
  });
});

describe('browserTimeZone', () => {
  const originalDateTimeFormat = Intl.DateTimeFormat;

  afterEach(() => {
    Intl.DateTimeFormat = originalDateTimeFormat;
  });

  it('returns the resolved IANA zone', () => {
    expect(typeof browserTimeZone()).toBe('string');
    expect(browserTimeZone().length).toBeGreaterThan(0);
  });

  it('falls back to UTC when Intl throws', () => {
    // @ts-expect-error -- deliberately breaking Intl to exercise the fallback
    Intl.DateTimeFormat = () => {
      throw new Error('no Intl');
    };
    expect(browserTimeZone()).toBe('UTC');
  });
});

describe('listTimeZones', () => {
  const originalSupportedValuesOf = Intl.supportedValuesOf;

  beforeEach(() => {
    Intl.supportedValuesOf = originalSupportedValuesOf;
  });

  afterEach(() => {
    Intl.supportedValuesOf = originalSupportedValuesOf;
  });

  it('uses Intl.supportedValuesOf when available', () => {
    const zones = listTimeZones();
    expect(zones).toContain('America/New_York');
    expect(zones.length).toBeGreaterThan(10);
  });

  it('falls back to a static list when Intl.supportedValuesOf is unavailable', () => {
    // @ts-expect-error -- simulate a runtime without this newer Intl API
    Intl.supportedValuesOf = undefined;
    const zones = listTimeZones();
    expect(zones).toContain('UTC');
    expect(zones).toContain('America/New_York');
    expect(zones.length).toBeGreaterThan(0);
  });

  it('spot-checks a handful of common zones are present in the fallback list', () => {
    // @ts-expect-error -- simulate a runtime without this newer Intl API
    Intl.supportedValuesOf = undefined;
    const zones = listTimeZones();
    for (const zone of ['Europe/London', 'Asia/Tokyo', 'Australia/Sydney']) {
      expect(zones).toContain(zone);
    }
  });
});

describe('interoperability', () => {
  it('a slot rendered via formatInTZ("time") matches the hour implied by tzOffsetLabel', () => {
    // 14:00 UTC in a zone at GMT-4 should read as 10:00 local.
    const label = tzOffsetLabel('America/New_York', new Date(SUMMER));
    expect(label).toBe('GMT-4');
    expect(formatInTZ(SUMMER, 'America/New_York', 'time')).toBe('10:00 AM');
  });
});
