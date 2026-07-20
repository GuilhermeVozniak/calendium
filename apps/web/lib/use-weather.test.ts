import * as React from 'react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { renderHook, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { mockForecast } from '@/lib/calendar-mock';
import { forecastByDate, useWeather, WEATHER_DAYS } from '@/lib/use-weather';

const getWeatherMock = vi.fn();
vi.mock('@/lib/api', () => ({
  getApiClient: () => ({ getWeather: (...args: unknown[]) => getWeatherMock(...args) }),
}));

const useInstanceMock = vi.fn();
vi.mock('@/lib/use-instance', () => ({ useInstance: () => useInstanceMock() }));

const fetchCalendarPrefsMock = vi.fn();
vi.mock('@/lib/calendar-prefs-data', () => ({
  fetchCalendarPrefs: () => fetchCalendarPrefsMock(),
}));

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

// Gating seam (M2.8 Task 5 × 13): useWeather reads CalendarPrefs from
// GET /v1/prefs/calendar — weatherEnabled (opt-in, default false) plus the
// nullable homeLat/homeLon home location.
describe('useWeather gating (calendar prefs seam)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    window.localStorage.clear();
    useInstanceMock.mockReturnValue({ data: { capabilities: { weather: true } } });
    getWeatherMock.mockResolvedValue(mockForecast(WEATHER_DAYS, new Date(2026, 6, 19)));
  });

  function renderWeather() {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    return renderHook(() => useWeather(), {
      wrapper: ({ children }) => React.createElement(QueryClientProvider, { client }, children),
    });
  }

  it('stays hidden on the opt-in default (weatherEnabled=false)', async () => {
    fetchCalendarPrefsMock.mockResolvedValue({ weatherEnabled: false, homeLat: 52.4, homeLon: 4.9 });
    const { result } = renderWeather();
    await waitFor(() => expect(fetchCalendarPrefsMock).toHaveBeenCalled());
    await new Promise((r) => setTimeout(r, 20));
    expect(getWeatherMock).not.toHaveBeenCalled();
    expect(result.current.byDate).toBeUndefined();
  });

  it('fetches for the home location when weatherEnabled and both coordinates are set', async () => {
    fetchCalendarPrefsMock.mockResolvedValue({ weatherEnabled: true, homeLat: 52.4, homeLon: 4.9 });
    const { result } = renderWeather();
    await waitFor(() => expect(result.current.byDate).toBeDefined());
    expect(getWeatherMock).toHaveBeenCalledWith(52.4, 4.9, expect.any(String), WEATHER_DAYS);
  });

  it('falls back to cached browser geolocation when home coordinates are unset', async () => {
    window.localStorage.setItem('calendium.weather-geo', JSON.stringify({ lat: 1.5, lon: 2.5 }));
    fetchCalendarPrefsMock.mockResolvedValue({ weatherEnabled: true, homeLat: null, homeLon: null });
    const { result } = renderWeather();
    await waitFor(() => expect(result.current.byDate).toBeDefined());
    expect(getWeatherMock).toHaveBeenCalledWith(1.5, 2.5, expect.any(String), WEATHER_DAYS);
  });

  it('stays hidden when only one home coordinate is set and no geolocation exists', async () => {
    fetchCalendarPrefsMock.mockResolvedValue({ weatherEnabled: true, homeLat: 52.4, homeLon: null });
    const { result } = renderWeather();
    await waitFor(() => expect(fetchCalendarPrefsMock).toHaveBeenCalled());
    await new Promise((r) => setTimeout(r, 20));
    expect(getWeatherMock).not.toHaveBeenCalled();
    expect(result.current.byDate).toBeUndefined();
  });

  it('hides when the server capability is off, even with the pref on', async () => {
    useInstanceMock.mockReturnValue({ data: { capabilities: { weather: false } } });
    fetchCalendarPrefsMock.mockResolvedValue({ weatherEnabled: true, homeLat: 52.4, homeLon: 4.9 });
    const { result } = renderWeather();
    await waitFor(() => expect(fetchCalendarPrefsMock).toHaveBeenCalled());
    await new Promise((r) => setTimeout(r, 20));
    expect(getWeatherMock).not.toHaveBeenCalled();
    expect(result.current.byDate).toBeUndefined();
  });
});
