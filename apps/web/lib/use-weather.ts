'use client';

import * as React from 'react';
import { useQuery } from '@tanstack/react-query';
import { ApiRequestError, type DayForecast } from '@calendium/shared';

import { getApiClient } from '@/lib/api';
import { mockForecast } from '@/lib/calendar-mock';
import { fetchCalendarPrefs } from '@/lib/calendar-prefs-data';
import { DEMO_MODE } from '@/lib/demo';
import { useInstance } from '@/lib/use-instance';

/**
 * Inline weather for calendar day headers (M2.8 Task 13). Weather is opt-in:
 * CalendarPrefs.weatherEnabled (GET /v1/prefs/calendar, M2.8 Task 5) defaults
 * to false, so chips appear only after the user enables the toggle in
 * Settings → Calendar automation. Best-effort decoration with fail-soft
 * everywhere: pref off, no location, server capability off, endpoint 501, or
 * any vendor failure all resolve to `byDate: undefined` (chips hidden) —
 * never an error state on the calendar.
 */

/** Days fetched per location: covers day, week, and the agenda strip. */
export const WEATHER_DAYS = 14;

/** Freshness window, mirroring the server's 30-minute forecast cache. */
export const WEATHER_STALE_MS = 30 * 60 * 1000;

const GEO_CACHE_KEY = 'calendium.weather-geo';

/** Fallback demo-mode location (San Francisco) so demo browsing shows chips. */
const DEMO_LOCATION: WeatherLocation = { lat: 37.77, lon: -122.42 };

export interface WeatherLocation {
  lat: number;
  lon: number;
}

/** The browser-permission-gated geolocation cached from a previous grant. */
export function readCachedGeo(): WeatherLocation | null {
  if (typeof window === 'undefined') return null;
  try {
    const raw = window.localStorage.getItem(GEO_CACHE_KEY);
    if (!raw) return null;
    const parsed: unknown = JSON.parse(raw);
    const loc = parsed as Partial<WeatherLocation>;
    if (typeof loc.lat === 'number' && typeof loc.lon === 'number') {
      return { lat: loc.lat, lon: loc.lon };
    }
  } catch {
    // Unreadable cache ≡ no cache.
  }
  return null;
}

function cacheGeo(loc: WeatherLocation): void {
  try {
    window.localStorage.setItem(GEO_CACHE_KEY, JSON.stringify(loc));
  } catch {
    // Best-effort cache only.
  }
}

/** Keys a forecast list by its `date` (YYYY-MM-DD) for O(1) per-day lookup. */
export function forecastByDate(rows: DayForecast[] | null | undefined): Map<string, DayForecast> {
  const map = new Map<string, DayForecast>();
  for (const row of rows ?? []) map.set(row.date, row);
  return map;
}

export interface UseWeatherResult {
  /** Day-keyed (yyyy-MM-dd) forecasts, or undefined when weather is hidden. */
  byDate: Map<string, DayForecast> | undefined;
}

export function useWeather(enabled = true): UseWeatherResult {
  const instanceQuery = useInstance();
  // Calendar automation prefs (M2.8 Task 5). Same query key as the settings
  // section so both consumers share one cache entry; this module only READS
  // prefs (it never writes them).
  const prefsQuery = useQuery({
    queryKey: ['calendar-prefs'],
    queryFn: fetchCalendarPrefs,
    enabled,
    staleTime: WEATHER_STALE_MS,
    retry: false,
  });
  const prefs = prefsQuery.data;

  // Server capability gate: hide when the server says weather is off. Older
  // servers omit the block entirely — treat that as available and let the
  // endpoint's 501 be the fallback signal.
  const capabilityOff = instanceQuery.data?.capabilities?.weather === false;
  // Opt-in: weatherEnabled defaults to false server-side, and unknown prefs
  // (still loading / errored) keep the chips hidden rather than flashing.
  const weatherOn = prefs?.weatherEnabled === true;

  // Home location wins when both coordinates are set (nullable server-side);
  // otherwise fall back to browser geolocation below.
  const homeLocation: WeatherLocation | null =
    typeof prefs?.homeLat === 'number' && typeof prefs?.homeLon === 'number'
      ? { lat: prefs.homeLat, lon: prefs.homeLon }
      : null;

  const [geo, setGeo] = React.useState<WeatherLocation | null>(readCachedGeo);
  const wantGeo = enabled && !capabilityOff && weatherOn && !homeLocation && !geo;
  React.useEffect(() => {
    if (!wantGeo || typeof navigator === 'undefined' || !navigator.geolocation) return;
    navigator.geolocation.getCurrentPosition(
      (pos) => {
        const loc = { lat: pos.coords.latitude, lon: pos.coords.longitude };
        cacheGeo(loc);
        setGeo(loc);
      },
      () => {
        // Permission denied / unavailable: weather simply stays hidden.
      },
      { maximumAge: 60 * 60 * 1000, timeout: 10_000 }
    );
  }, [wantGeo]);

  const location = homeLocation ?? geo ?? (DEMO_MODE ? DEMO_LOCATION : null);

  const tz = React.useMemo(() => {
    try {
      return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC';
    } catch {
      return 'UTC';
    }
  }, []);

  const query = useQuery<DayForecast[] | null>({
    queryKey: ['weather', location?.lat ?? null, location?.lon ?? null, tz],
    enabled: enabled && !capabilityOff && weatherOn && location !== null,
    // Honest freshness: a shown chip is never staler than the server's own
    // 30-minute cache window.
    staleTime: WEATHER_STALE_MS,
    retry: false,
    queryFn: async (): Promise<DayForecast[] | null> => {
      if (!location) return null;
      try {
        return await getApiClient().getWeather(location.lat, location.lon, tz, WEATHER_DAYS);
      } catch (err) {
        // 501 = no vendor configured server-side: hide, don't error-loop.
        if (err instanceof ApiRequestError && err.status === 501) return null;
        if (DEMO_MODE) return mockForecast(WEATHER_DAYS);
        // Best-effort decoration: any other failure hides the chips instead
        // of surfacing an error state on the calendar.
        return null;
      }
    },
  });

  const byDate = React.useMemo(
    () => (query.data && query.data.length > 0 ? forecastByDate(query.data) : undefined),
    [query.data]
  );

  return { byDate };
}
