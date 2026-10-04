import useAuth, { AuthProvider } from '@/context/auth';
import { api } from '@/lib/api';
import { startAppStateFocus } from '@/lib/app-focus';
import { startOutboxReplay } from '@/lib/offline';
import { persistOptions, queryClient } from '@/lib/query-client';
import { ServerConfigProvider, useServerConfig } from '@/lib/server-config';
import { applyNamedTheme, loadStoredNamedTheme, navThemeFor, useNamedTheme } from '@/lib/theme';
import { THEME_NAMES } from '@calendium/shared';
import { ThemeProvider } from '@react-navigation/native';
import { PersistQueryClientProvider } from '@tanstack/react-query-persist-client';
import { useColorScheme } from 'nativewind';
import * as React from 'react';

/**
 * Applies the stored server theme preference on sign-in (M2.6 Task 13): the
 * server value wins over local storage when they differ; unreachable servers
 * and demo mode leave the locally stored theme in place.
 */
function NamedThemeSync() {
  const { isAuthenticated } = useAuth();
  const { config } = useServerConfig();
  const demoMode = config?.demoMode ?? false;

  React.useEffect(() => {
    if (!isAuthenticated || demoMode) return;
    let cancelled = false;
    api
      .getPreferences()
      .then((prefs) => {
        if (cancelled || !(THEME_NAMES as readonly string[]).includes(prefs.theme)) return;
        applyNamedTheme(prefs.theme);
      })
      .catch(() => {
        // Offline / unreachable — the locally stored theme already applies.
      });
    return () => {
      cancelled = true;
    };
  }, [isAuthenticated, demoMode]);

  return null;
}

export default function Providers({ children }: { children: React.ReactNode }) {
  const { colorScheme } = useColorScheme();
  const namedTheme = useNamedTheme();

  // Offline outbox: replay queued triage when reachability returns or the app
  // foregrounds. Started here so it spans the app's whole lifetime.
  React.useEffect(() => startOutboxReplay(queryClient), []);

  // Foregrounding the app counts as react-query "focus" (refetches stale queries).
  React.useEffect(() => startAppStateFocus(), []);

  // Restore the persisted named theme before the server preference lands.
  React.useEffect(() => {
    void loadStoredNamedTheme();
  }, []);

  return (
    // ServerConfig sits ABOVE auth so the Better Auth/API clients are built from
    // the runtime-discovered server before auth reads them.
    <ServerConfigProvider>
      <AuthProvider>
        {/* PersistQueryClientProvider restores the persisted cache (AsyncStorage)
            before rendering, so the last-known mail/calendar shows offline. */}
        <PersistQueryClientProvider client={queryClient} persistOptions={persistOptions}>
          <ThemeProvider value={navThemeFor(namedTheme, colorScheme ?? 'light')}>
            <NamedThemeSync />
            {/* the app */}
            {children}
          </ThemeProvider>
        </PersistQueryClientProvider>
      </AuthProvider>
    </ServerConfigProvider>
  );
}
