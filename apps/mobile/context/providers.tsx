import { AuthProvider } from '@/context/auth';
import { queryClient } from '@/lib/query-client';
import { ServerConfigProvider } from '@/lib/server-config';
import { NAV_THEME } from '@/lib/theme';
import { ThemeProvider } from '@react-navigation/native';
import { QueryClientProvider } from '@tanstack/react-query';
import { useColorScheme } from 'nativewind';

export default function Providers({ children }: { children: React.ReactNode }) {
  const { colorScheme } = useColorScheme();

  return (
    // ServerConfig sits ABOVE auth so the Better Auth/API clients are built from
    // the runtime-discovered server before auth reads them.
    <ServerConfigProvider>
      <AuthProvider>
        <QueryClientProvider client={queryClient}>
          <ThemeProvider value={NAV_THEME[colorScheme ?? 'light']}>
            {/* the app */}
            {children}
          </ThemeProvider>
        </QueryClientProvider>
      </AuthProvider>
    </ServerConfigProvider>
  );
}
