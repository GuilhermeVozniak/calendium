import { THEME_NAMES, type ThemeName } from '@calendium/shared';
import AsyncStorage from '@react-native-async-storage/async-storage';
import { DarkTheme, DefaultTheme, type Theme } from '@react-navigation/native';
import { useSyncExternalStore } from 'react';
 
export const THEME = {
  light: {
    background: 'hsl(0 0% 100%)',
    foreground: 'hsl(0 0% 3.9%)',
    card: 'hsl(0 0% 100%)',
    cardForeground: 'hsl(0 0% 3.9%)',
    popover: 'hsl(0 0% 100%)',
    popoverForeground: 'hsl(0 0% 3.9%)',
    primary: 'hsl(0 0% 9%)',
    primaryForeground: 'hsl(0 0% 98%)',
    secondary: 'hsl(0 0% 96.1%)',
    secondaryForeground: 'hsl(0 0% 9%)',
    muted: 'hsl(0 0% 96.1%)',
    mutedForeground: 'hsl(0 0% 45.1%)',
    accent: 'hsl(0 0% 96.1%)',
    accentForeground: 'hsl(0 0% 9%)',
    destructive: 'hsl(0 84.2% 60.2%)',
    border: 'hsl(0 0% 89.8%)',
    input: 'hsl(0 0% 89.8%)',
    ring: 'hsl(0 0% 63%)',
    radius: '0.625rem',
    chart1: 'hsl(12 76% 61%)',
    chart2: 'hsl(173 58% 39%)',
    chart3: 'hsl(197 37% 24%)',
    chart4: 'hsl(43 74% 66%)',
    chart5: 'hsl(27 87% 67%)',
  },
  dark: {
    background: 'hsl(0 0% 3.9%)',
    foreground: 'hsl(0 0% 98%)',
    card: 'hsl(0 0% 3.9%)',
    cardForeground: 'hsl(0 0% 98%)',
    popover: 'hsl(0 0% 3.9%)',
    popoverForeground: 'hsl(0 0% 98%)',
    primary: 'hsl(0 0% 98%)',
    primaryForeground: 'hsl(0 0% 9%)',
    secondary: 'hsl(0 0% 14.9%)',
    secondaryForeground: 'hsl(0 0% 98%)',
    muted: 'hsl(0 0% 14.9%)',
    mutedForeground: 'hsl(0 0% 63.9%)',
    accent: 'hsl(0 0% 14.9%)',
    accentForeground: 'hsl(0 0% 98%)',
    destructive: 'hsl(0 70.9% 59.4%)',
    border: 'hsl(0 0% 14.9%)',
    input: 'hsl(0 0% 14.9%)',
    ring: 'hsl(300 0% 45%)',
    radius: '0.625rem',
    chart1: 'hsl(220 70% 50%)',
    chart2: 'hsl(160 60% 45%)',
    chart3: 'hsl(30 80% 55%)',
    chart4: 'hsl(280 65% 60%)',
    chart5: 'hsl(340 75% 55%)',
  },
};
 
type Palette = typeof THEME.light;

/**
 * Named theme palettes (M2.6 Task 13). Each named theme overrides the same
 * nine tokens as the CSS blocks in apps/web/app/globals.css and
 * apps/desktop/frontend/src/styles.css — values are shared verbatim (hsl
 * numbers identical); the neutral base supplies the rest.
 */
const NAMED_OVERRIDES: Record<Exclude<ThemeName, 'neutral'>, { light: Partial<Palette>; dark: Partial<Palette> }> = {
  ocean: {
    light: {
      background: 'hsl(210 40% 99%)',
      primary: 'hsl(217 72% 46%)',
      primaryForeground: 'hsl(210 40% 98%)',
      secondary: 'hsl(214 45% 94%)',
      accent: 'hsl(214 45% 93%)',
      accentForeground: 'hsl(217 60% 25%)',
      muted: 'hsl(214 40% 94%)',
      border: 'hsl(214 32% 88%)',
      ring: 'hsl(217 72% 55%)',
    },
    dark: {
      background: 'hsl(218 35% 8%)',
      primary: 'hsl(213 80% 66%)',
      primaryForeground: 'hsl(218 35% 10%)',
      secondary: 'hsl(216 28% 16%)',
      accent: 'hsl(216 28% 18%)',
      accentForeground: 'hsl(213 70% 85%)',
      muted: 'hsl(216 28% 15%)',
      border: 'hsl(216 26% 18%)',
      ring: 'hsl(213 80% 60%)',
    },
  },
  forest: {
    light: {
      background: 'hsl(150 40% 99%)',
      primary: 'hsl(158 55% 34%)',
      primaryForeground: 'hsl(150 40% 98%)',
      secondary: 'hsl(154 45% 94%)',
      accent: 'hsl(154 45% 93%)',
      accentForeground: 'hsl(158 60% 25%)',
      muted: 'hsl(154 40% 94%)',
      border: 'hsl(154 32% 88%)',
      ring: 'hsl(158 55% 43%)',
    },
    dark: {
      background: 'hsl(156 35% 8%)',
      primary: 'hsl(152 45% 60%)',
      primaryForeground: 'hsl(156 35% 10%)',
      secondary: 'hsl(154 28% 16%)',
      accent: 'hsl(154 28% 18%)',
      accentForeground: 'hsl(152 70% 85%)',
      muted: 'hsl(154 28% 15%)',
      border: 'hsl(154 26% 18%)',
      ring: 'hsl(152 45% 54%)',
    },
  },
  sunset: {
    light: {
      background: 'hsl(24 40% 99%)',
      primary: 'hsl(24 82% 48%)',
      primaryForeground: 'hsl(24 40% 98%)',
      secondary: 'hsl(26 45% 94%)',
      accent: 'hsl(26 45% 93%)',
      accentForeground: 'hsl(24 60% 25%)',
      muted: 'hsl(26 40% 94%)',
      border: 'hsl(26 32% 88%)',
      ring: 'hsl(24 82% 57%)',
    },
    dark: {
      background: 'hsl(22 35% 8%)',
      primary: 'hsl(27 90% 62%)',
      primaryForeground: 'hsl(22 35% 10%)',
      secondary: 'hsl(24 28% 16%)',
      accent: 'hsl(24 28% 18%)',
      accentForeground: 'hsl(27 70% 85%)',
      muted: 'hsl(24 28% 15%)',
      border: 'hsl(24 26% 18%)',
      ring: 'hsl(27 90% 56%)',
    },
  },
};

/** Full palette (light+dark) per named theme; 'neutral' is the base THEME. */
export const THEMES: Record<ThemeName, { light: Palette; dark: Palette }> = {
  neutral: THEME,
  ocean: {
    light: { ...THEME.light, ...NAMED_OVERRIDES.ocean.light },
    dark: { ...THEME.dark, ...NAMED_OVERRIDES.ocean.dark },
  },
  forest: {
    light: { ...THEME.light, ...NAMED_OVERRIDES.forest.light },
    dark: { ...THEME.dark, ...NAMED_OVERRIDES.forest.dark },
  },
  sunset: {
    light: { ...THEME.light, ...NAMED_OVERRIDES.sunset.light },
    dark: { ...THEME.dark, ...NAMED_OVERRIDES.sunset.dark },
  },
};

// ---------------------------------------------------------------------------
// Named-theme store: module-level state + subscribers, persisted to
// AsyncStorage. The server preference (GET /v1/me/preferences) is applied on
// sign-in via applyNamedTheme and wins over the stored value; AsyncStorage is
// the offline fallback.
// ---------------------------------------------------------------------------

const NAMED_THEME_STORAGE_KEY = 'calendium.namedTheme';

let currentNamedTheme: ThemeName = 'neutral';
const listeners = new Set<() => void>();

export function getNamedTheme(): ThemeName {
  return currentNamedTheme;
}

export function subscribeNamedTheme(onChange: () => void): () => void {
  listeners.add(onChange);
  return () => listeners.delete(onChange);
}

/** Applies a named theme (ignoring unknown values) and persists it locally. */
export function applyNamedTheme(name: ThemeName): void {
  if (!(THEME_NAMES as readonly string[]).includes(name)) return;
  currentNamedTheme = name;
  void AsyncStorage.setItem(NAMED_THEME_STORAGE_KEY, name).catch(() => {
    // Storage unavailable — the theme still applies for this session.
  });
  for (const listener of listeners) listener();
}

/** Restores the locally stored named theme (app start, before sign-in). */
export async function loadStoredNamedTheme(): Promise<ThemeName> {
  try {
    const stored = await AsyncStorage.getItem(NAMED_THEME_STORAGE_KEY);
    if (stored && (THEME_NAMES as readonly string[]).includes(stored)) {
      currentNamedTheme = stored as ThemeName;
      for (const listener of listeners) listener();
    }
  } catch {
    // Storage unavailable — stay on the current (neutral) theme.
  }
  return currentNamedTheme;
}

/** React hook: the active named theme, updating on palette switches. */
export function useNamedTheme(): ThemeName {
  return useSyncExternalStore(subscribeNamedTheme, getNamedTheme, getNamedTheme);
}

/** React Navigation theme derived from a named palette + color scheme. */
export function navThemeFor(name: ThemeName, scheme: 'light' | 'dark'): Theme {
  const palette = THEMES[name][scheme];
  const base = scheme === 'dark' ? DarkTheme : DefaultTheme;
  return {
    ...base,
    colors: {
      ...base.colors,
      background: palette.background,
      border: palette.border,
      card: palette.card,
      notification: palette.destructive,
      primary: palette.primary,
      text: palette.foreground,
    },
  };
}

export const NAV_THEME: Record<'light' | 'dark', Theme> = {
  light: {
    ...DefaultTheme,
    colors: {
      background: THEME.light.background,
      border: THEME.light.border,
      card: THEME.light.card,
      notification: THEME.light.destructive,
      primary: THEME.light.primary,
      text: THEME.light.foreground,
    },
  },
  dark: {
    ...DarkTheme,
    colors: {
      background: THEME.dark.background,
      border: THEME.dark.border,
      card: THEME.dark.card,
      notification: THEME.dark.destructive,
      primary: THEME.dark.primary,
      text: THEME.dark.foreground,
    },
  },
};