import '@/global.css';

import Providers from '@/context/providers';
import { PortalHost } from '@rn-primitives/portal';
import { Stack } from 'expo-router';
import { StatusBar } from 'expo-status-bar';
import { useColorScheme } from 'nativewind';
import { GestureHandlerRootView } from 'react-native-gesture-handler';

export {
  // Catch any errors thrown by the Layout component.
  ErrorBoundary,
} from 'expo-router';

export default function RootLayout() {
  const { colorScheme } = useColorScheme();

  return (
    <GestureHandlerRootView style={{ flex: 1 }}>
      <Providers>
        <StatusBar style={colorScheme === 'dark' ? 'light' : 'dark'} />
        <Stack>
          <Stack.Screen name="connect" options={{ headerShown: false }} />
          <Stack.Screen name="(tabs)" options={{ headerShown: false }} />
          <Stack.Screen name="thread/[id]" options={{ headerShown: false }} />
          <Stack.Screen name="compose" options={{ presentation: 'modal', headerShown: false }} />
          <Stack.Screen name="auth-callback" options={{ headerShown: false }} />
        </Stack>
        <PortalHost />
      </Providers>
    </GestureHandlerRootView>
  );
}
