import { AuthProvider } from '@/context/auth';
import { NAV_THEME } from '@/lib/theme';
import { ThemeProvider } from '@react-navigation/native';
import { useColorScheme } from 'nativewind';

export default function Providers({ children }: { children: React.ReactNode }) {
  const { colorScheme } = useColorScheme();

  return (
    <AuthProvider>
      <ThemeProvider value={NAV_THEME[colorScheme ?? 'light']}>
        {/* the app */}
        {children}
      </ThemeProvider>
    </AuthProvider>
  );
}
