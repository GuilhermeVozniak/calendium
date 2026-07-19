import { describe, expect, it } from 'vitest';

import { mockForecast } from '@/lib/calendar-mock';
import { forecastByDate, WEATHER_DAYS } from '@/lib/use-weather';

describe('forecastByDate', () => {
  it('keys rows by their YYYY-MM-DD date', () => {
    const rows = mockForecast(5, new Date(2026, 6, 19));
    const map = forecastByDate(rows);
    expect(map.size).toBe(5);
    expect(map.get('2026-07-19')).toEqual(rows[0]);
    expect(map.get('2026-07-23')).toEqual(rows[4]);
    expect(map.get('2026-08-01')).toBeUndefined();
  });

  it('tolerates null/undefined input', () => {
    expect(forecastByDate(null).size).toBe(0);
    expect(forecastByDate(undefined).size).toBe(0);
  });
});

describe('mockForecast (demo data)', () => {
  it('is deterministic and covers sequential days', () => {
    const from = new Date(2026, 6, 19);
    const a = mockForecast(WEATHER_DAYS, from);
    const b = mockForecast(WEATHER_DAYS, from);
    expect(a).toEqual(b);
    expect(a).toHaveLength(14);
    expect(a[0]!.date).toBe('2026-07-19');
    expect(a[13]!.date).toBe('2026-08-01');
    for (const day of a) {
      expect(day.precipChance).toBeGreaterThanOrEqual(0);
      expect(day.precipChance).toBeLessThanOrEqual(100);
      expect(day.highCelsius).toBeGreaterThan(day.lowCelsius);
    }
  });

  it('clamps to the 1..14 window GET /v1/weather serves', () => {
    expect(mockForecast(0, new Date(2026, 6, 19))).toHaveLength(1);
    expect(mockForecast(99, new Date(2026, 6, 19))).toHaveLength(14);
  });
});
