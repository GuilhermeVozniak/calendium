import { AuthProvider } from '@/context/auth';
import { startOutboxReplay } from '@/lib/offline';
import { persistOptions, queryClient } from '@/lib/query-client';
import { ServerConfigProvider } from '@/lib/server-config';
import { NAV_THEME } from '@/lib/theme';
import { ThemeProvider } from '@react-navigation/native';
import { PersistQueryClientProvider } from '@tanstack/react-query-persist-client';
import { useColorScheme } from 'nativewind';
import * as React from 'react';

export default function Providers({ children }: { children: React.ReactNode }) {
  const { colorScheme } = useColorScheme();

  // Offline outbox: replay queued triage when reachability returns or the app
  // foregrounds. Started here so it spans the app's whole lifetime.
  React.useEffect(() => startOutboxReplay(queryClient), []);

  return (
    // ServerConfig sits ABOVE auth so the Better Auth/API clients are built from
    // the runtime-discovered server before auth reads them.
    <ServerConfigProvider>
      <AuthProvider>
        {/* PersistQueryClientProvider restores the persisted cache (AsyncStorage)
            before rendering, so the last-known mail/calendar shows offline. */}
        <PersistQueryClientProvider client={queryClient} persistOptions={persistOptions}>
          <ThemeProvider value={NAV_THEME[colorScheme ?? 'light']}>
            {/* the app */}
            {children}
          </ThemeProvider>
        </PersistQueryClientProvider>
      </AuthProvider>
    </ServerConfigProvider>
  );
}
