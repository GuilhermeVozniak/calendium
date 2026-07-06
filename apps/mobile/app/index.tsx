import AppleIcon from '@/assets/icons/apple.svg';
import GoogleIcon from '@/assets/icons/google.svg';
import { Button } from '@/components/ui/button';
import { Icon } from '@/components/ui/icon';
import { Text } from '@/components/ui/text';
import useAuth from '@/context/auth';
import { useServerConfig } from '@/lib/server-config';
import { Redirect, Stack } from 'expo-router';
import { CalendarRangeIcon, MoonStarIcon, SunIcon } from 'lucide-react-native';
import { useColorScheme } from 'nativewind';
import * as React from 'react';
import { ActivityIndicator, View } from 'react-native';

const SCREEN_OPTIONS = {
  title: '',
  headerTransparent: true,
  headerRight: () => <ThemeToggle />,
};

export default function SignInScreen() {
  const { isConfigured, isLoading: serverLoading } = useServerConfig();
  const { user, loading: authLoading, signInWithOAuth } = useAuth();
  const [loading, setLoading] = React.useState(false);

  const handleSocialLogin = async (provider: 'google' | 'apple') => {
    try {
      setLoading(true);
      await signInWithOAuth(provider);
    } catch {
      // Error already surfaced by the auth context.
    } finally {
      setLoading(false);
    }
  };

  if (serverLoading || authLoading) {
    return (
      <>
        <Stack.Screen options={SCREEN_OPTIONS} />
        <View className="flex-1 items-center justify-center bg-background">
          <ActivityIndicator size="large" />
        </View>
      </>
    );
  }

  // Server discovery gates sign-in: pick a server before authenticating.
  if (!isConfigured) {
    return <Redirect href="/connect" />;
  }

  if (user) {
    return <Redirect href="/(tabs)/inbox" />;
  }

  return (
    <>
      <Stack.Screen options={SCREEN_OPTIONS} />
      <View className="flex-1 items-center justify-center gap-10 bg-background p-6">
        {/* Calendium brand mark */}
        <View className="items-center gap-4">
          <View className="size-16 items-center justify-center rounded-2xl bg-primary shadow-sm shadow-black/10">
            <Icon as={CalendarRangeIcon} className="size-8 text-primary-foreground" />
          </View>
          <View className="items-center gap-1.5">
            <Text variant="h1">Calendium</Text>
            <Text className="text-center text-sm text-muted-foreground">
              Email and calendar, at the speed of thought.
            </Text>
          </View>
        </View>

        {/* Social sign-in */}
        <View className="w-full max-w-xs gap-3">
          <Button
            onPress={() => handleSocialLogin('google')}
            disabled={loading}
            className="flex-row items-center gap-3">
            <GoogleIcon width={20} height={20} />
            <Text>Continue with Google</Text>
          </Button>

          <Button
            onPress={() => handleSocialLogin('apple')}
            disabled={loading}
            variant="outline"
            className="flex-row items-center gap-3">
            <AppleIcon width={20} height={20} />
            <Text>Continue with Apple</Text>
          </Button>

          {loading && <ActivityIndicator className="mt-2" />}
        </View>
      </View>
    </>
  );
}

const THEME_ICONS = {
  light: SunIcon,
  dark: MoonStarIcon,
};

function ThemeToggle() {
  const { colorScheme, toggleColorScheme } = useColorScheme();

  return (
    <Button
      onPressIn={toggleColorScheme}
      size="icon"
      variant="ghost"
      className="ios:size-9 rounded-full web:mx-4">
      <Icon as={THEME_ICONS[colorScheme ?? 'light']} className="size-5" />
    </Button>
  );
}
