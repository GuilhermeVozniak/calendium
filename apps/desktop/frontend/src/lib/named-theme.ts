import { THEME_NAMES, THEME_SWATCHES, type ThemeName } from '@calendium/shared';

import { api } from '@/lib/api';
import { isDemoMode } from '@/lib/server-config';
import { errorMessage, toast } from '@/lib/toast';

export { THEME_NAMES, THEME_SWATCHES };
export type { ThemeName };

const STORAGE_KEY = 'calendium-named-theme';

/** The locally stored named theme, defaulting to 'neutral' on absence/garbage. */
export function getStoredNamedTheme(): ThemeName {
  try {
    const stored = window.localStorage.getItem(STORAGE_KEY);
    if (stored && (THEME_NAMES as readonly string[]).includes(stored)) return stored as ThemeName;
  } catch {
    // localStorage unavailable — fall through to neutral.
  }
  return 'neutral';
}

/** neutral → delete html.dataset.theme; else set it (see styles.css blocks). */
export function applyNamedTheme(name: ThemeName): void {
  const root = document.documentElement;
  if (name === 'neutral') delete root.dataset.theme;
  else root.dataset.theme = name;
}

/** Applies the stored named theme at startup (called from main.tsx). */
export function initNamedTheme(): void {
  applyNamedTheme(getStoredNamedTheme());
}

function persist(name: ThemeName): void {
  try {
    window.localStorage.setItem(STORAGE_KEY, name);
  } catch {
    // localStorage unavailable — the theme still applies for this session.
  }
}

/**
 * Applies + persists locally, then fire-and-forget PUT /v1/me/preferences.
 * A failed PUT keeps the local theme (no fake success) and toasts, except in
 * explicit demo mode where no server exists.
 */
export function setNamedTheme(name: ThemeName): void {
  applyNamedTheme(name);
  persist(name);
  if (isDemoMode()) return;
  api.updatePreferences({ theme: name }).catch((e) => {
    toast({
      title: "Couldn't save your theme",
      description: errorMessage(e),
      variant: 'destructive',
    });
  });
}

/**
 * Fetches the server preference once; the server value wins over localStorage
 * when they differ (localStorage is the offline fallback). Returns the applied
 * theme, or null when unavailable (demo mode, offline, signed out).
 */
export async function syncNamedThemeFromServer(): Promise<ThemeName | null> {
  try {
    if (isDemoMode()) return null;
    const prefs = await api.getPreferences();
    if (!(THEME_NAMES as readonly string[]).includes(prefs.theme)) return null;
    applyNamedTheme(prefs.theme);
    persist(prefs.theme);
    return prefs.theme;
  } catch {
    return null;
  }
}
