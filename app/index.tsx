import AppleIcon from '@/assets/icons/apple.svg';
import GoogleIcon from '@/assets/icons/google.svg';
import { Button } from '@/components/ui/button';
import { Icon } from '@/components/ui/icon';
import { Text } from '@/components/ui/text';
import useAuth from '@/context/auth';
import { Stack } from 'expo-router';
import { MoonStarIcon, SunIcon } from 'lucide-react-native';
import { useColorScheme } from 'nativewind';
import * as React from 'react';
import { useState } from 'react';
import { ActivityIndicator, Image, View, type ImageStyle } from 'react-native';

const LOGO = {
  light: require('@/assets/images/react-native-reusables-light.png'),
  dark: require('@/assets/images/react-native-reusables-dark.png'),
};

const SCREEN_OPTIONS = {
  // title: 'React Native Reusables',
  headerTransparent: true,
  headerRight: () => <ThemeToggle />,
};

const IMAGE_STYLE: ImageStyle = {
  height: 76,
  width: 76,
};

export default function Screen() {
  const { colorScheme } = useColorScheme();
  const { user, loading: authLoading, signInWithOAuth, signOut } = useAuth();
  const [loading, setLoading] = useState(false);

  const handleSocialLogin = async (provider: 'google' | 'apple') => {
    try {
      setLoading(true);
      await signInWithOAuth(provider);
    } catch (error) {
      // Error already handled in context
    } finally {
      setLoading(false);
    }
  };

  const handleSignOut = async () => {
    try {
      setLoading(true);
      await signOut();
    } catch (error) {
      // Error already handled in context
    } finally {
      setLoading(false);
    }
  };

  if (authLoading) {
    return (
      <>
        <Stack.Screen options={SCREEN_OPTIONS} />
        <View className="flex-1 items-center justify-center">
          <ActivityIndicator size="large" />
        </View>
      </>
    );
  }

  return (
    <>
      <Stack.Screen options={SCREEN_OPTIONS} />
      <View className="flex-1 items-center justify-center gap-8 p-4">
        <Image source={LOGO[colorScheme ?? 'light']} style={IMAGE_STYLE} resizeMode="contain" />
        {!user && (
          <View className="gap-2 p-4">
            {/* Social Login Buttons */}
            <View className="w-full max-w-xs gap-4">
              <Text className="text-center text-lg font-semibold">Sign in with</Text>

              <Button
                onPress={() => handleSocialLogin('google')}
                disabled={loading}
                className="flex-row items-center gap-3">
                <GoogleIcon width={20} height={20} />
                <Text>Continue with Google</Text>
              </Button>

              {/* <Button
                onPress={() => handleSocialLogin('apple')}
                disabled={loading}
                variant="outline"
                className="flex-row items-center gap-3">
                <AppleIcon width={20} height={20} />
                <Text>Continue with Apple</Text>
              </Button> */}
            </View>
          </View>
        )}

        {user && (
          <View className="gap-4 p-4">
            <Text className="text-center text-lg font-semibold">Welcome back!</Text>
            <Text className="text-center text-sm">{user.email}</Text>
            <Button onPress={handleSignOut} disabled={loading} variant="outline">
              <Text>Sign Out</Text>
            </Button>
          </View>
        )}
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
