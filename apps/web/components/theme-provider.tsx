'use client';

import * as React from 'react';
import { THEME_NAMES, type ThemeName } from '@calendium/shared';
import { toast } from 'sonner';

import { getApiClient } from '@/lib/api';
import { DEMO_MODE } from '@/lib/demo';

type Theme = 'light' | 'dark' | 'system';
type ResolvedTheme = 'light' | 'dark';

const STORAGE_KEY = 'calendium-theme';
const NAMED_THEME_STORAGE_KEY = 'calendium-named-theme';

export type { ThemeName };

interface ThemeContextValue {
  /** The user's preference, including 'system'. */
  theme: Theme;
  /** What is actually applied to the document right now. */
  resolvedTheme: ResolvedTheme;
  setTheme: (theme: Theme) => void;
  /** The active named palette ('neutral' = base token set, no attribute). */
  namedTheme: ThemeName;
  /**
   * Applies data-theme, persists to localStorage immediately, then
   * fire-and-forget PUT /v1/me/preferences (errors toast outside demo mode).
   */
  setNamedTheme: (name: ThemeName) => void;
}

const ThemeContext = React.createContext<ThemeContextValue | null>(null);

function getSystemTheme(): ResolvedTheme {
  if (typeof window === 'undefined') return 'light';
  return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
}

function readStoredTheme(): Theme {
  if (typeof window === 'undefined') return 'system';
  try {
    const stored = window.localStorage.getItem(STORAGE_KEY);
    if (stored === 'light' || stored === 'dark' || stored === 'system') return stored;
  } catch {
    // localStorage unavailable (private mode, etc.)
  }
  return 'system';
}

function readStoredNamedTheme(): ThemeName {
  if (typeof window === 'undefined') return 'neutral';
  try {
    const stored = window.localStorage.getItem(NAMED_THEME_STORAGE_KEY);
    if (stored && (THEME_NAMES as readonly string[]).includes(stored)) return stored as ThemeName;
  } catch {
    // localStorage unavailable (private mode, etc.)
  }
  return 'neutral';
}

function persistNamedTheme(name: ThemeName) {
  try {
    window.localStorage.setItem(NAMED_THEME_STORAGE_KEY, name);
  } catch {
    // localStorage unavailable — the theme still applies for this session.
  }
}

/**
 * Minimal class-strategy theme provider (no dependency on next-themes).
 * Works with the pre-paint init script in app/layout.tsx: both toggle the
 * `dark` class on <html> and keep `color-scheme` in sync.
 */
export function ThemeProvider({ children }: { children: React.ReactNode }) {
  const [theme, setThemeState] = React.useState<Theme>('system');
  const [resolvedTheme, setResolvedTheme] = React.useState<ResolvedTheme>('light');
  const [namedTheme, setNamedThemeState] = React.useState<ThemeName>('neutral');

  // Hydrate the preference from localStorage after mount (SSR-safe).
  React.useEffect(() => {
    setThemeState(readStoredTheme());
  }, []);

  /** neutral → delete html.dataset.theme; else set it. */
  const applyNamed = React.useCallback((name: ThemeName) => {
    const root = document.documentElement;
    if (name === 'neutral') delete root.dataset.theme;
    else root.dataset.theme = name;
    setNamedThemeState(name);
  }, []);

  // On app mount: apply the stored named theme, then fetch the server
  // preference once — the server value wins over localStorage when they
  // differ (localStorage is the offline fallback, synced back on change).
  React.useEffect(() => {
    applyNamed(readStoredNamedTheme());
    if (DEMO_MODE) return;
    let cancelled = false;
    getApiClient()
      .getPreferences()
      .then((prefs) => {
        if (cancelled || !(THEME_NAMES as readonly string[]).includes(prefs.theme)) return;
        applyNamed(prefs.theme);
        persistNamedTheme(prefs.theme);
      })
      .catch(() => {
        // Signed out / offline — the localStorage value already applies.
      });
    return () => {
      cancelled = true;
    };
  }, [applyNamed]);

  const apply = React.useCallback((preference: Theme) => {
    const resolved = preference === 'system' ? getSystemTheme() : preference;
    const root = document.documentElement;
    root.classList.toggle('dark', resolved === 'dark');
    root.style.colorScheme = resolved;
    setResolvedTheme(resolved);
  }, []);

  React.useEffect(() => {
    apply(theme);
    if (theme !== 'system') return;
    const media = window.matchMedia('(prefers-color-scheme: dark)');
    const onChange = () => apply('system');
    media.addEventListener('change', onChange);
    return () => media.removeEventListener('change', onChange);
  }, [theme, apply]);

  const setTheme = React.useCallback((next: Theme) => {
    setThemeState(next);
    try {
      window.localStorage.setItem(STORAGE_KEY, next);
    } catch {
      // localStorage unavailable — theme still applies for this session.
    }
  }, []);

  const setNamedTheme = React.useCallback(
    (name: ThemeName) => {
      applyNamed(name);
      persistNamedTheme(name);
      if (DEMO_MODE) return;
      // Fire-and-forget server persistence — on failure the local theme is
      // kept (no fake success) and the user is told the sync failed.
      getApiClient()
        .updatePreferences({ theme: name })
        .catch(() => {
          toast.error("Couldn't save your theme to the server");
        });
    },
    [applyNamed]
  );

  const value = React.useMemo(
    () => ({ theme, resolvedTheme, setTheme, namedTheme, setNamedTheme }),
    [theme, resolvedTheme, setTheme, namedTheme, setNamedTheme]
  );

  return <ThemeContext.Provider value={value}>{children}</ThemeContext.Provider>;
}

export function useTheme(): ThemeContextValue {
  const ctx = React.useContext(ThemeContext);
  if (!ctx) throw new Error('useTheme must be used within a <ThemeProvider>');
  return ctx;
}
